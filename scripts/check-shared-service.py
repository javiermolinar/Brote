#!/usr/bin/env python3
"""Real Delve lifecycle check. Builds isolated fixtures; requires Go and dlv."""
import json
import os
from pathlib import Path
import select
import signal
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def run():
    with tempfile.TemporaryDirectory(prefix="brote-service-") as temporary:
        work = Path(temporary).resolve()
        env = dict(os.environ, DEBUG_HANDOVER_HOME=str(work / "sessions"),
                   AGENTDEBUGGER_DATA_DIR=str(work / "data"))
        if not os.environ.get("BROTE_CHECK_OTLP"):
            env = {key: value for key, value in env.items() if not key.startswith("OTEL_EXPORTER_OTLP")}
        core = Path(os.environ["BROTE_CHECK_CORE"]).resolve() if os.environ.get("BROTE_CHECK_CORE") else work / "brote"
        if not os.environ.get("BROTE_CHECK_CORE"):
            subprocess.run(["go", "build", "-o", str(core), "./cmd/brote"], cwd=ROOT, check=True)
        fixture = work / "fixture"
        (fixture / ".vscode").mkdir(parents=True)
        (fixture / "go.mod").write_text("module servicefixture\n\ngo 1.23\n")
        (fixture / "main.go").write_text('package main\nimport "time"\nfunc main(){for {time.Sleep(time.Second)}}\n')
        (fixture / "main_test.go").write_text('package main\nimport "testing"\nfunc TestSelected(t *testing.T){t.Log("selected")}\nfunc TestExcluded(t *testing.T){t.Fatal("must not run")}\n')
        config = {"name": "Selected test", "type": "go", "request": "launch",
                  "mode": "test", "program": str(fixture), "buildFlags": "-trimpath",
                  "args": ["-test.run=^TestSelected$"],
                  "substitutePath": [{"from": str(fixture), "to": "servicefixture"}]}
        (fixture / ".vscode" / "launch.json").write_text(json.dumps({"configurations": [config]}))
        target_binary = work / "target"
        subprocess.run(["go", "build", "-gcflags=all=-N -l", "-o", str(target_binary), "."],
                       cwd=fixture, check=True)
        live = set()
        editor = None
        target = None

        def cli(*args):
            result = subprocess.run([str(core), *args], env=env, capture_output=True, text=True, timeout=50)
            if result.returncode:
                raise RuntimeError(result.stderr)
            return json.loads(result.stdout)

        def stop_tracing():
            try:
                pid = int((work / "data/tracing/service.pid").read_text())
                os.kill(pid, signal.SIGTERM)
            except (OSError, ValueError):
                return
            for _ in range(200):
                try: os.kill(pid, 0)
                except ProcessLookupError: return
                time.sleep(.1)
            raise RuntimeError("fixture tracing service did not stop")

        def service(sid, operation, **payload):
            descriptor = json.loads((work / "sessions" / sid / "session.json").read_text())
            state = cli("state", sid, "--summary")
            request = dict(version=1, session=sid, run=state["run"], generation=state["generation"], pauseEpoch=state["pauseEpoch"],
                           client="integration", commandId=str(time.monotonic_ns()), operation=operation, **payload)
            req = urllib.request.Request(descriptor["http"] + "/api/v1", data=json.dumps(request).encode(),
                                         headers={"Authorization": "Bearer " + descriptor["token"], "Content-Type": "application/json"})
            return json.load(urllib.request.urlopen(req, timeout=10))

        def start(*args):
            value = cli("start", "--thread", "", "--project", str(fixture), *args)
            assert value.get("run") and value.get("serviceVersion")==1
            assert "#" not in value["panel"]
            live.add(value["id"])
            return value["id"]

        try:
            # Both explicit opt-outs retain the old contract without relabelling it.
            for optout in ('--legacy', '--service=false'):
                old=cli('start',optout,'--no-ui','--thread','','--project',str(fixture),'--binary',str(target_binary))
                live.add(old['id']);assert old.get('serviceVersion',0)==0 and not old.get('run'),old
                cli('end-session',old['id'],'--confirmed');live.remove(old['id'])
            stubdir=work/"stub-bin";stubdir.mkdir()
            codex_stub=stubdir/"codex";codex_stub.write_text("#!/bin/sh\nexit 1\n");codex_stub.chmod(0o700)
            warning_env=dict(env,PATH=str(stubdir)+os.pathsep+env["PATH"])
            warning=subprocess.run([str(core),"start","--service","--binary",str(target_binary),"--project",str(fixture),"--thread","00000000-0000-0000-0000-000000000000"],env=warning_env,capture_output=True,text=True,timeout=35)
            assert warning.returncode==0,warning.stderr
            warning_start=json.loads(warning.stdout);live.add(warning_start["id"])
            warning_record=json.loads((work/"sessions"/warning_start["id"]/"session.json").read_text())
            assert warning_start["notificationError"] and warning_start["run"]==warning_record["run"] and warning_start["serviceVersion"]==1
            assert warning_record["token"] not in warning.stdout
            cli("end-session",warning_start["id"],"--confirmed");live.remove(warning_start["id"])

            sid = start("--binary", str(target_binary))
            descriptor = json.loads((work / "sessions" / sid / "session.json").read_text())
            try:
                urllib.request.urlopen(descriptor["http"] + "/api/health", timeout=3)
                raise AssertionError("unauthenticated health accepted")
            except urllib.error.HTTPError as error:
                assert error.code == 401
            discovered = cli("sessions")
            assert descriptor["token"] not in json.dumps(discovered)
            assert next(s for s in discovered if s["id"] == sid)["run"] == descriptor["run"]
            state = cli("state", sid, "--summary")
            rejected_detach=subprocess.run([str(core),"detach",sid,"--human"],env=env,capture_output=True,text=True,timeout=10)
            assert rejected_detach.returncode and json.loads(rejected_detach.stderr)["code"]=="unsupported_operation"
            assert cli("state",sid)["state"]["Pid"]==state["state"]["Pid"]
            assert not cli("capabilities",sid)["capabilities"]["service"]["detach"]
            editor = subprocess.Popen([str(core), "dap", sid], stdin=subprocess.PIPE,
                                      stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
            pending = b""

            def dap_request(sequence, command, arguments=None):
                nonlocal pending
                body = json.dumps({"seq": sequence, "type": "request", "command": command,
                                   "arguments": arguments or {}}).encode()
                editor.stdin.write(b"Content-Length: " + str(len(body)).encode() + b"\r\n\r\n" + body)
                editor.stdin.flush()
                deadline = time.monotonic() + 15
                while True:
                    if b"\r\n\r\n" in pending:
                        header, payload = pending.split(b"\r\n\r\n", 1)
                        size = int(header.split(b":", 1)[1])
                        if len(payload) >= size:
                            message = json.loads(payload[:size])
                            pending = payload[size:]
                            if message.get("type") == "response" and message.get("request_seq") == sequence:
                                return message
                            continue
                    remaining = deadline - time.monotonic()
                    assert remaining > 0 and select.select([editor.stdout], [], [], remaining)[0], "DAP timeout"
                    chunk = os.read(editor.stdout.fileno(), 65536)
                    assert chunk, "DAP ended unexpectedly"
                    pending += chunk

            assert dap_request(1, "initialize", {"adapterID": "go"})["success"]
            assert dap_request(2, "attach")["success"]
            assert dap_request(3, "configurationDone")["success"]
            assert not dap_request(4, "setVariable", {"variablesReference": 1, "name": "x", "value": "1"})["success"]
            point = dap_request(5, "setBreakpoints", {"source": {"path": str(fixture / "main.go")}, "breakpoints": [{"line": 3}]})
            assert point["success"] and point["body"]["breakpoints"][0]["verified"], point
            stored = cli("state", sid)["definitions"]["items"]
            assert len(stored) == 1 and stored[0]["owner"] == "editor"
            stable_id = stored[0]["id"]
            assert dap_request(6, "setBreakpoints", {"source": {"path": str(fixture / "main.go")}, "breakpoints": [{"line": 3}]})["success"]
            assert cli("state", sid)["definitions"]["items"][0]["id"] == stable_id
            assert dap_request(7, "setBreakpoints", {"source": {"path": str(fixture / "main.go")}, "breakpoints": []})["success"]
            assert dap_request(8, "disconnect")["success"]
            editor.stdin.close()
            editor.wait(timeout=5)
            editor = None
            assert cli("state", sid)["state"]["Pid"] == state["state"]["Pid"]
            cli("continue", sid, "--human", "--command-id", "real-continue")
            cli("pause", sid, "--human", "--wait", "10s")
            assert cli("state", sid)["status"] == "paused"
            duplicate = subprocess.run([str(core), "continue", sid, "--human", "--command-id", "real-continue"],
                                       env=env, capture_output=True, text=True, timeout=10)
            assert duplicate.returncode and "already submitted" in duplicate.stderr
            restarted = cli("restart", sid, "--human")
            assert restarted["id"] == sid and restarted["run"] != descriptor["run"]
            assert cli("state", sid)["state"]["Pid"] != state["state"]["Pid"]
            cli("continue", sid, "--human", "--command-id", "survive-recovery")
            cli("pause", sid, "--human", "--wait", "10s")
            before_recovery = cli("state", sid)
            record = json.loads((work / "sessions" / sid / "session.json").read_text())
            os.kill(record["brokerPid"], signal.SIGKILL)
            time.sleep(.2)
            cli("recover", sid)
            recovered = cli("state", sid)
            assert recovered["run"] == before_recovery["run"] and recovered["state"]["Pid"] == before_recovery["state"]["Pid"]
            duplicate = subprocess.run([str(core), "continue", sid, "--human", "--command-id", "survive-recovery"],
                                       env=env, capture_output=True, text=True, timeout=10)
            assert duplicate.returncode and "already submitted" in duplicate.stderr
            binding = recovered["binding"]
            task = cli("task-start", sid, "--binding", binding["id"], "--revision", str(binding["revision"]),
                       "--instruction", "Validate bounded execution in the isolated test fixture")["task"]
            wrong = subprocess.run([str(core), "continue", sid, "--binding", "wrong-client", "--task", task["id"]],
                                   env=env, capture_output=True, text=True, timeout=10)
            assert wrong.returncode and "binding mismatch" in wrong.stderr
            bounded = subprocess.run([str(core), "task-execute", sid, "--binding", binding["id"], "--task", task["id"],
                                      "--operation", "continue", "--wait", "250ms"],
                                     env=env, capture_output=True, text=True, timeout=15)
            assert bounded.returncode and "timed out" in bounded.stderr
            for _ in range(50):
                settled = cli("state", sid)
                if settled["status"] == "paused":
                    break
                time.sleep(.1)
            assert settled["status"] == "paused" and settled["task"]["status"] == "cancelled"
            renewed = subprocess.run([str(core), "task-heartbeat", sid, "--binding", binding["id"], "--task", task["id"]],
                                     env=env, capture_output=True, text=True, timeout=10)
            assert renewed.returncode
            cli("end-session", sid, "--confirmed")
            live.remove(sid)

            one = start("--binary", str(target_binary))
            two = start("--binary", str(target_binary))
            before_two = cli("state", two)
            first_descriptor = json.loads((work / "sessions" / one / "session.json").read_text())
            cross = urllib.request.Request(first_descriptor["http"]+"/api/sessions/stop",data=json.dumps({"id":two,"confirmed":True}).encode(),headers={"Authorization":"Bearer "+first_descriptor["token"],"Content-Type":"application/json"})
            try:
                urllib.request.urlopen(cross, timeout=5)
                raise AssertionError("cross-session stop accepted")
            except urllib.error.HTTPError as error:
                assert error.code==403
            assert cli("state",two)["state"]["Pid"]==before_two["state"]["Pid"]
            cli("continue", one, "--human")
            cli("pause", one, "--human", "--wait", "10s")
            assert cli("state", two)["state"]["Pid"] == before_two["state"]["Pid"]
            assert cli("state", two)["status"] == "paused"
            cli("end-session", one, "--confirmed")
            live.remove(one)
            assert cli("state", two)["state"]["Pid"] == before_two["state"]["Pid"]
            cli("end-session", two, "--confirmed")
            live.remove(two)

            target = subprocess.Popen([str(target_binary)])
            invalid_editor_attach=subprocess.run([str(core),"start","--service","--editor-start","--pid",str(target.pid),"--binary",str(target_binary),"--project",str(fixture),"--thread",""],env=env,capture_output=True,text=True,timeout=10)
            assert invalid_editor_attach.returncode and "process attach is unsupported" in invalid_editor_attach.stderr
            assert target.poll() is None
            sid = start("--pid", str(target.pid), "--binary", str(target_binary))
            attached = cli("state", sid)
            assert attached["state"]["Pid"] == target.pid and attached["debugger"]["mode"] == "attached"
            denied = subprocess.run([str(core), "restart", sid, "--human"], env=env,
                                    capture_output=True, text=True, timeout=10)
            assert denied.returncode and "externally attached" in denied.stderr
            cli("continue", sid, "--human")
            cli("detach", sid, "--human")
            live.remove(sid)
            time.sleep(.3)
            assert target.poll() is None
            process_state=subprocess.check_output(["ps","-o","stat=","-p",str(target.pid)],text=True).strip()
            assert "T" not in process_state, "detached process is still stopped"
            target.terminate()
            target.wait(timeout=5)
            target = None

            # Force failure at descriptor publication, after Delve/backend initialization.
            # Owned launch is killed; externally attached target must survive.
            for attach in (False, True):
                failed_id = "failedattach" if attach else "failedlaunch"
                failed_dir=work/"sessions"/failed_id
                (failed_dir/"session.json").mkdir(parents=True)
                if attach:
                    target=subprocess.Popen([str(target_binary)])
                args=[str(core),"serve","--service","--backend","dap","--id",failed_id,"--binary",str(target_binary),"--project",str(fixture),"--dlv",shutil.which("dlv") or str(Path.home()/"go/bin/dlv")]
                if attach:args += ["--pid",str(target.pid)]
                failed=subprocess.run(args,env=env,capture_output=True,text=True,timeout=35)
                assert failed.returncode and "session.json" in failed.stderr, failed.stderr
                assert "API server listening" in (failed_dir/"delve.log").read_text()
                assert not (failed_dir/"session.json").is_file()
                if attach:
                    assert target.poll() is None
                    target.terminate();target.wait(timeout=5);target=None
                else:
                    rows=subprocess.check_output(["ps","-ax","-o","command="],text=True).splitlines()
                    assert not any(row.strip()==str(target_binary) for row in rows), "failed launch leaked its target"

            abandoned = start("--editor-start", "--binary", str(target_binary))
            abandoned_pid=cli("state",abandoned)["state"]["Pid"]
            for _ in range(350):
                record=json.loads((work/"sessions"/abandoned/"session.json").read_text())
                if record.get("stopped"):break
                time.sleep(.1)
            assert record.get("stopped"), "unconfigured editor launch did not expire"
            time.sleep(.2)
            try:
                os.kill(abandoned_pid,0)
                raise AssertionError("abandoned target remains alive")
            except ProcessLookupError:pass
            live.remove(abandoned)

            sid = start("--config", "Selected test", "--build")
            cli("break", sid, "--human", "--file", str(fixture / "main_test.go"), "--line", "3")
            for iteration in range(2):
                cli("continue", sid, "--human", "--wait", "10s")
                paused = cli("state", sid)
                assert paused["status"] == "paused" and paused["source"]["file"] == str(fixture / "main_test.go")
                cli("continue", sid, "--human", "--wait", "10s")
                state = cli("state", sid)
                assert state["status"] == "exited" and state["state"]["exitStatus"] == 0
                if iteration == 0:
                    old_run = state["run"]
                    assert cli("restart", sid, "--human")["run"] != old_run
            cli("end-session", sid, "--confirmed")
            live.remove(sid)
            (fixture / "main.go").write_text('package main\nimport "fmt"\nfunc main(){\n for value:=7;value<10;value++ {\n fmt.Println(value)\n }\n}\n')
            subprocess.run(["go", "build", "-gcflags=all=-N -l", "-o", str(target_binary), "."], cwd=fixture, check=True)
            sid = start("--binary", str(target_binary))
            saved = cli("tracepoint", "add", sid, "--file", str(fixture / "main.go"), "--line", "5", "--name", "observe value", "--values", '{"selected_value":"value"}', "--capture-limit", "1")
            assert saved["resolutions"][0]["verified"], saved
            cli("continue", sid, "--human", "--wait", "10s")
            for _ in range(50):
                captures = cli("captures", sid)
                if len(captures["captures"]) >= 2:
                    break
                time.sleep(.1)
            assert [c["status"] for c in captures["captures"]] == ["captured", "skipped"], captures
            assert cli("state", sid)["status"] == "paused"
            point_id = saved["definitions"]["items"][0]["id"]
            listed = cli("tracepoint", "list", sid)
            assert listed["definitions"]["items"][0]["id"] == point_id
            assert cli("capabilities", sid)["capabilities"]["service"]["tracepoints"]
            assert cli("goroutines", sid, "--count", "1")["result"]["Goroutines"]
            assert len(cli("stack", sid, "--count", "1")["result"]["Locations"]) == 1
            updated = cli("tracepoint", "update", sid, "--id", point_id, "--revision", "1", "--enabled=false")
            assert not updated["definitions"]["items"][0]["enabled"]
            stale = subprocess.run([str(core), "tracepoint", "remove", sid, "--id", point_id, "--revision", "1"], env=env, capture_output=True, text=True, timeout=10)
            assert stale.returncode and json.loads(stale.stderr)["code"]=="stale_revision" and json.loads(stale.stderr)["version"]==1
            assert not cli("tracepoint", "remove", sid, "--id", point_id, "--revision", "2")["definitions"]["items"]
            first = captures["captures"][0]
            assert first["values"]["selected_value"]["value"] == "7", first
            assert first["snapshot"]["frames"] and first["goroutine"] > 0
            if os.environ.get("BROTE_CHECK_OTLP"):
                assert first["exportStatus"] in ("queued", "sent"), first
                program_trace = first["programTraceId"]
                debugger_trace = first["debuggerTraceId"]
                assert program_trace != debugger_trace
            else:
                assert first["exportStatus"] in ("queued", "sent"), first
            cli("end-session", sid, "--confirmed")
            live.remove(sid)

            if os.environ.get("BROTE_CHECK_OTLP"):
                query = os.environ.get("BROTE_TEMPO_QUERY_URL", "http://127.0.0.1:3200")
                exported = []
                for trace_id in (program_trace, debugger_trace):
                    for _ in range(60):
                        try:
                            request = urllib.request.Request(query + "/api/traces/" + trace_id, headers={"Accept": "application/json"})
                            trace = json.load(urllib.request.urlopen(request, timeout=5))
                            batches = trace.get("batches", trace.get("resourceSpans", []))
                            spans = [span for batch in batches for scope in batch.get("scopeSpans", batch.get("instrumentationLibrarySpans", [])) for span in scope.get("spans", [])]
                            if len(spans) >= 3:
                                exported.extend(spans)
                                break
                        except urllib.error.HTTPError:
                            pass
                        time.sleep(.2)
                    else:
                        raise AssertionError("Tempo trace retrieval timed out: " + trace_id)
                attrs = lambda span: {a["key"]: a["value"].get("stringValue", a["value"].get("intValue")) for a in span["attributes"]}
                captures = [span for span in exported if attrs(span).get("program.span.type") == "snapshot"]
                assert len(captures) == 1 and attrs(captures[0])["program.value.selected_value"] == "7", exported
                assert captures[0]["startTimeUnixNano"] == captures[0]["endTimeUnixNano"]
                parent = next(span for span in exported if span["spanId"] == captures[0]["parentSpanId"])
                assert attrs(parent)["program.span.type"] == "thread"
                assert any(span["spanId"] == parent["parentSpanId"] and attrs(span)["program.span.type"] == "run" for span in exported)
                assert any(span["name"] == "debugger.session" for span in exported)
                print(json.dumps({"tempoRetrieved": True, "programTraceId": program_trace, "debuggerTraceId": debugger_trace, "exactValue": "7"}))

            stop_tracing()  # Remote settings belong to the shared tracing process.
            # A bound but non-listening local socket deterministically refuses OTLP.
            with socket.socket() as unavailable:
                unavailable.bind(("127.0.0.1", 0))
                old_endpoint = env.get("OTEL_EXPORTER_OTLP_ENDPOINT")
                env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://127.0.0.1:" + str(unavailable.getsockname()[1])
                sid = start("--binary", str(target_binary))
                cli("tracepoint", "add", sid, "--file", str(fixture / "main.go"), "--line", "5", "--name", "unavailable export", "--values", '{"value":"value"}', "--capture-limit", "1")
                cli("continue", sid, "--human", "--wait", "10s")
                for _ in range(50):
                    outcomes = cli("captures", sid)["captures"]
                    if outcomes and outcomes[0]["exportStatus"] == "failed":
                        break
                    time.sleep(.1)
                assert outcomes[0]["status"] == "captured" and outcomes[0]["exportStatus"] == "failed", outcomes
                assert cli("state", sid)["status"] == "paused"
                started = time.monotonic()
                cli("end-session", sid, "--confirmed")
                assert time.monotonic() - started < 8
                live.remove(sid)
                if old_endpoint is None:
                    env.pop("OTEL_EXPORTER_OTLP_ENDPOINT")
                else:
                    env["OTEL_EXPORTER_OTLP_ENDPOINT"] = old_endpoint

            stop_tracing()
            (fixture / "main.go").write_text('package main\nfunc main(){panic("service panic evidence")}\n')
            subprocess.run(["go", "build", "-gcflags=all=-N -l", "-o", str(target_binary), "."], cwd=fixture, check=True)
            sid = start("--binary", str(target_binary))
            cli("continue", sid, "--human", "--wait", "10s")
            evidence = cli("state", sid)
            assert evidence["status"] == "paused" and evidence["state"]["stopReason"] == "exception", evidence
            assert evidence["frames"] and "service panic evidence" in json.dumps(evidence.get("exception")), evidence
            cli("end-session", sid, "--confirmed")
            live.remove(sid)
            print(json.dumps({"launchedDetachRejectedSafely": True, "warningStartupIdentity": True, "abandonedEditorStartupCleaned": True, "crossSessionStopDenied": True, "partialLaunchCleanup": True, "failedAttachPreservesTarget": True, "simultaneousSessionsIsolated": True, "unavailableOTLPReported": True, "cliTracepointCRUD": True, "cliPagedInspection": True, "guardedAutoContinue": True, "boundedTracepointCapture": True, "exactSelectedValue": True, "captureLimitPreservesPause": True, "editorDefinitionReplay": True, "panicEvidence": True, "boundedAgentExecution": True, "cancelledScopeCannotRenew": True, "pauseRunningTarget": True, "duplicateCommandRejected": True, "authenticatedDiscovery": True, "stdioDAP": True,
                              "disconnectPreservesTarget": True, "restartNewRun": True, "recoveryPreservesRunAndPID": True,
                              "attachDetachPreservesPID": True, "attachedRestartRejected": True,
                              "selectedGoTest": True, "trimpathSourceMapping": True,
                              "breakpointsRestoredOnRestart": True}))
        finally:
            if editor is not None:
                editor.kill()
                editor.wait(timeout=5)
            for sid in live:
                cli("end-session", sid, "--confirmed")
            if target is not None and target.poll() is None:
                target.terminate()
                target.wait(timeout=5)
            stop_tracing()


if __name__ == "__main__":
    run()
