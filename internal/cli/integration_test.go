package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentdebugger/internal/dap"
	"agentdebugger/internal/delve"
	"agentdebugger/internal/editors/zed"
	"agentdebugger/internal/session"
)

type testDAP struct {
	t      *testing.T
	c      net.Conn
	r      *bufio.Reader
	seq    int
	events []obj
}

func (d *testDAP) receive() obj {
	d.t.Helper()
	_ = d.c.SetReadDeadline(time.Now().Add(10 * time.Second))
	v, e := dap.Read(d.r)
	if e != nil {
		d.t.Fatal(e)
	}
	return v
}

func (d *testDAP) request(command string, args obj) obj {
	d.t.Helper()
	d.seq++
	if e := dap.Write(d.c, obj{"seq": d.seq, "type": "request", "command": command, "arguments": args}); e != nil {
		d.t.Fatal(e)
	}
	for {
		v := d.receive()
		if str(v["type"]) == "event" {
			d.events = append(d.events, v)
			continue
		}
		if num(v["request_seq"]) != d.seq {
			d.t.Fatalf("unexpected reply %v", v)
		}
		return v
	}
}

func (d *testDAP) ok(command string, args obj) obj {
	d.t.Helper()
	v := d.request(command, args)
	if !truth(v["success"]) {
		d.t.Fatalf("%s: %v", command, v)
	}
	return asObj(v["body"])
}

func (d *testDAP) event(name string) obj {
	d.t.Helper()
	for {
		for i, v := range d.events {
			if str(v["event"]) == name {
				d.events = append(d.events[:i], d.events[i+1:]...)
				return asObj(v["body"])
			}
		}
		d.events = append(d.events, d.receive())
	}
}

func newTestDAP(t *testing.T, s session.Descriptor) *testDAP {
	t.Helper()
	c, e := net.Dial("tcp", s.DAP)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	d := &testDAP{t: t, c: c, r: bufio.NewReader(c)}
	d.ok("initialize", obj{"adapterID": "go", "clientID": "handover-test", "pathFormat": "path", "linesStartAt1": true, "columnsStartAt1": true, "supportsVariableType": true})
	d.ok("attach", obj{"mode": "remote", "stopOnEntry": false})
	d.event("initialized")
	return d
}

func TestIntegrationRoundTrip(t *testing.T) {
	if os.Getenv("DH_INTEGRATION") != "1" {
		t.Skip("set DH_INTEGRATION=1 to test real Delve")
	}
	for _, mode := range []string{"main-goroutine", "worker-goroutine"} {
		t.Run(mode, func(t *testing.T) { testRoundTrip(t, mode == "worker-goroutine") })
	}
}

func testRoundTrip(t *testing.T, worker bool) {
	dir := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(dir, "sessions"))
	t.Setenv("AGENTDEBUGGER_DATA_DIR", filepath.Join(dir, "history"))
	t.Setenv("CODEX_THREAD_ID", "")
	helper := filepath.Join(dir, "debug-handover")
	cmd := exec.Command("go", "build", "-race", "-o", helper, "../../cmd/debug-handover")
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build helper: %s: %v", out, e)
	}
	project := filepath.Join(dir, "demo")
	if e := os.Mkdir(project, 0755); e != nil {
		t.Fatal(e)
	}
	source, e := os.ReadFile("../../examples/demo/main.go")
	if e != nil {
		t.Fatal(e)
	}
	if worker {
		source = []byte(strings.Replace(string(source), "func main() {", "func main() { done := make(chan struct{}); go func() { worker(); close(done) }(); <-done }\n\nfunc worker() {", 1))
	}
	source = []byte(strings.Replace(string(source), "func process(attempt, total int) int {", "func process(attempt, total int) int {\n samples := []struct { Name string; Values []int }{{\"first\", []int{1,2,3}}, {\"second\", []int{4,5,6}}}; _ = samples", 1))
	sourceLine := func(marker string) int {
		t.Helper()
		for i, line := range strings.Split(string(source), "\n") {
			if strings.Contains(line, marker) {
				return i + 1
			}
		}
		t.Fatalf("missing fixture marker %q", marker)
		return 0
	}
	breakLine := sourceLine("// HANDOVER:")
	stepLine := sourceLine("fmt.Printf")
	returnLine := sourceLine("return total")
	file := filepath.Join(project, "main.go")
	if e = os.WriteFile(file, source, 0644); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(project, "demo")
	cmd = exec.Command("go", "build", "-gcflags=all=-N -l", "-o", binary, file)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build fixture: %s: %v", out, e)
	}
	before, _ := os.ReadFile(binary)
	digest := sha256.Sum256(before)
	cmd = exec.Command(helper, "start", "--legacy", "--no-ui", "--binary", binary, "--project", project)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("start: %s: %v", out, e)
	}
	var result obj
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e)
	}
	s, e := session.Read(str(result["id"]))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		v, e := api(s, "GET", "/api/state?brief=1", nil)
		if e == nil {
			_, _ = api(s, "POST", "/api/action", obj{"action": "stop", "actor": "human", "binding": s.Binding.ID, "generation": v["generation"]})
		}
		time.Sleep(300 * time.Millisecond)
		log, _ := os.ReadFile(filepath.Join(s.Dir, "broker.log"))
		if strings.Contains(string(log), "DATA RACE") {
			t.Errorf("broker race: %s", log)
		}
	})
	state := func() obj {
		t.Helper()
		v, e := api(s, "GET", "/api/state", nil)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	action := func(name string, a obj) obj {
		t.Helper()
		v := state()
		if a == nil {
			a = obj{}
		}
		if a["actor"] == nil && (name == "continue" || name == "next" || name == "step" || name == "stepout" || name == "pause") {
			task := asObj(v["task"])
			if str(task["status"]) != "active" && str(task["status"]) != "authorized" {
				if _, err := api(s, "POST", "/api/action", obj{"action": "task-authorize", "actor": "human", "instruction": "Integration test: execute to the next assertion", "generation": v["generation"]}); err != nil {
					t.Fatal(err)
				}
				v = state()
				task = asObj(v["task"])
			}
			a["task"] = task["id"]
		}
		if name == "stop" {
			a["actor"] = "human"
		}
		a["action"] = name
		a["generation"] = v["generation"]
		a["binding"] = s.Binding.ID
		r, e := api(s, "POST", "/api/action", a)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		return r
	}
	waitPause := func() obj {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
			v := state()
			if str(v["status"]) == "paused" {
				return v
			}
		}
		t.Fatal("pause timed out")
		return nil
	}
	line := breakLine
	functionBP := action("break", obj{"function": "main.process"})
	action("clear", obj{"breakpoint": asObj(functionBP["Breakpoint"])["id"]})
	action("break", obj{"file": "main.go", "line": line, "condition": "attempt == 3", "hitCondition": "== 1"})
	grant := action("task-authorize", obj{"actor": "human", "instruction": "Integration: stop at the conditional breakpoint"})
	taskID := str(asObj(grant["task"])["id"])
	if output, err := exec.Command(helper, "task-execute", s.ID, "--task", taskID, "--binding", s.Binding.ID, "--operation", "continue", "--wait", "10s").CombinedOutput(); err != nil {
		t.Fatalf("bounded task execution: %s: %v", output, err)
	}
	paused := waitPause()
	if str(asObj(paused["task"])["delivery"]) != "acknowledged" {
		t.Fatal("bounded execution did not acknowledge task")
	}

	pid := num(asObj(paused["state"])["Pid"])
	gid := num(asObj(asObj(paused["state"])["currentGoroutine"])["id"])
	if worker && gid == 1 {
		t.Fatal("fixture did not stop in a worker goroutine")
	}
	check := func(s obj, wantLine int, wantTotal string) {
		t.Helper()
		if num(asObj(s["state"])["Pid"]) != pid {
			t.Fatal("process was replaced")
		}
		frames := asList(s["frames"])
		if len(frames) == 0 || num(asObj(frames[0])["line"]) != wantLine {
			t.Fatalf("wrong stop: %s", pretty(s["state"]))
		}
		vars := append(asList(asObj(frames[0])["Arguments"]), asList(asObj(frames[0])["Locals"])...)
		found := false
		for _, v := range vars {
			if str(asObj(v)["name"]) == "total" {
				found = true
				if str(asObj(v)["value"]) != wantTotal {
					t.Fatalf("total: %v", v)
				}
			}
		}
		if !found {
			t.Fatal("total missing")
		}
	}
	check(paused, line, "21")
	evaluated := action("eval", obj{"expression": "total + delta", "depth": 3, "count": 64})
	if str(asObj(evaluated["value"])["value"]) != "42" {
		t.Fatalf("bad expression value: %v", evaluated)
	}
	evaluated = action("eval", obj{"expression": "samples", "depth": 6, "count": 1})
	if num(asObj(evaluated["value"])["len"]) != 2 || len(asList(asObj(evaluated["value"])["children"])) != 1 {
		t.Fatalf("bounded deep inspection: %s", pretty(evaluated))
	}
	if _, e = api(s, "POST", "/api/action", obj{"action": "eval", "generation": state()["generation"], "expression": "process(1, 2)", "depth": 3, "count": 64}); e == nil {
		t.Fatal("function call accepted")
	}
	action("watch", obj{"expression": "total"})
	if _, err := api(s, "POST", "/api/action", obj{"action": "handover", "binding": s.Binding.ID, "generation": state()["generation"], "editor": "unknown"}); err == nil {
		t.Fatal("invalid editor accepted")
	}
	editor := "zed"
	if worker {
		editor = "vscode"
	}
	hand := action("handover", obj{"open": false, "editor": editor})
	if str(hand["owner"]) != editor {
		t.Fatalf("wrong editor: %v", hand)
	}
	if worker {
		if zed.HasProfile(s) {
			t.Fatal("VS Code handover wrote a Zed profile")
		}
		retry := action("handover", obj{"open": false, "editor": editor})
		if str(retry["handoverId"]) == str(hand["handoverId"]) {
			t.Fatal("retry did not create a fresh handover")
		}
		if _, err := api(s, "POST", "/api/action", obj{"action": "editor-error", "generation": state()["generation"], "handoverId": hand["handoverId"], "error": "stale"}); err == nil {
			t.Fatal("stale extension report accepted")
		}
	}
	d := newTestDAP(t, s)
	// A source breakpoint set from Zed must not erase the conditional Codex breakpoint.
	r := d.ok("setBreakpoints", obj{"source": obj{"path": file}, "breakpoints": []any{obj{"line": returnLine}}})
	if !truth(asObj(asList(r["breakpoints"])[0])["verified"]) {
		t.Fatal(r)
	}
	d.ok("configurationDone", obj{})
	if event := d.event("stopped"); num(event["threadId"]) != gid {
		t.Fatalf("attach focused wrong goroutine: want %d, event=%v", gid, event)
	}

	framesForReview := asList(d.ok("stackTrace", obj{"threadId": gid})["stackFrames"])
	frameForReview := asObj(framesForReview[0])["id"]
	d.ok("setBreakpoints", obj{"source": obj{"path": file}, "breakpoints": []any{obj{"line": returnLine}}})
	d.ok("scopes", obj{"frameId": frameForReview})
	attached := waitPause()
	if !truth(attached["editorReady"]) || str(attached["owner"]) != editor {
		t.Fatalf("editor did not finish attaching: %v", attached)
	}
	evaluated = action("eval", obj{"expression": "total", "depth": 3, "count": 64})
	if str(asObj(evaluated["value"])["value"]) != "21" {
		t.Fatal("observer evaluation failed")
	}
	check(attached, line, "21")
	count := 0
	for _, bp := range asList(attached["breakpoints"]) {
		if num(asObj(bp)["id"]) > 0 {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("breakpoints not preserved: %v", attached["breakpoints"])
	}
	if _, e = api(s, "POST", "/api/action", obj{"action": "next", "binding": s.Binding.ID, "generation": attached["generation"]}); e == nil || !strings.Contains(e.Error(), "task-start") {
		t.Fatalf("Agent executed without a task ID: %v", e)
	}
	d.ok("next", obj{"threadId": gid})
	d.event("stopped")
	stepped := waitPause()
	check(stepped, stepLine, "42")
	action("reclaim", nil)
	d.event("terminated")
	returned := state()
	check(returned, stepLine, "42")
	if str(returned["owner"]) != "agent" || truth(returned["editorConnected"]) {
		t.Fatal("ownership did not return")
	}
	if str(asObj(asObj(asList(returned["watches"])[0])["value"])["value"]) != "42" {
		t.Fatal("watch did not refresh after stepping")
	}

	// Run the actual detached listener with a Codex stub, never a real task.
	stubDir := filepath.Join(dir, "bridge-stub")
	if e = os.MkdirAll(stubDir, 0700); e != nil {
		t.Fatal(e)
	}
	queueLog := filepath.Join(dir, "queue.log")
	t.Setenv("DH_QUEUE_LOG", queueLog)
	if e = os.WriteFile(filepath.Join(stubDir, "codex"), []byte("#!/bin/sh\nif [ \"$2\" = --help ]; then echo --thread --message; exit 0; fi\nprintf '%s\\n' \"$*\" >> \"$DH_QUEUE_LOG\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	bind := exec.Command(helper, "bind", s.ID, "--thread", "11111111-1111-1111-1111-111111111111")
	if output, err := bind.CombinedOutput(); err != nil {
		t.Fatalf("bind bridge: %s: %v", output, err)
	}
	// Browser takes genuine ownership. Agent mutations are rejected; SSE returns
	// the human handback and a fresh state without changing PID or memory.
	browser := action("handover", obj{"editor": "browser", "open": false})
	if str(browser["owner"]) != "browser" {
		t.Fatal(browser)
	}
	if _, err := api(s, "POST", "/api/action", obj{"action": "next", "binding": s.Binding.ID, "generation": state()["generation"]}); err == nil {
		t.Fatal("agent stepped during browser ownership")
	}
	eventResult := make(chan session.Event, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		_ = stream(ctx, s, uint64(num(browser["cursor"])), s.Binding.ID, func(event session.Event) error {
			if event.Kind == "control_returned" {
				eventResult <- event
				return eventDone
			}
			return nil
		})
	}()
	action("reclaim", obj{"actor": "browser", "note": "check total"})
	select {
	case event := <-eventResult:
		if event.Note != "check total" || event.Owner != "agent" {
			t.Error(event)
		}
	case <-ctx.Done():
		t.Fatal("missing streamed handback")
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		current := state()
		if str(asObj(current["notification"])["status"]) == "queued" {
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(filepath.Join(s.Dir, "bridge.log"))
			t.Fatalf("bridge not queued: %s %s", pretty(current["notification"]), data)
		}
	}
	queued, _ := os.ReadFile(queueLog)
	if !strings.Contains(string(queued), s.ID) {
		t.Fatal("bridge did not deliver session event")
	}
	check(state(), stepLine, "42")
	taskGrant := action("task-authorize", obj{"actor": "human", "instruction": "Inspect the current pause without stepping"})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if str(asObj(state()["task"])["delivery"]) == "queued" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Codex bridge did not queue authorized task")
		}
	}
	queued, _ = os.ReadFile(queueLog)
	if !strings.Contains(string(queued), "task-execute") {
		t.Fatal("missing scoped task instructions")
	}
	action("task-cancel", obj{"actor": "human", "task": asObj(taskGrant["task"])["id"]})
	// Restore the previous preferred editor for the remaining recovery checks.
	action("handover", obj{"editor": editor, "open": false})
	action("reclaim", nil)
	// Kill only the broker. Delve and its paused target must survive, and the
	// replacement broker must retain watches, breakpoints, token, and stop PC.
	pc := asObj(asObj(returned["state"])["currentThread"])["pc"]
	reviewFunctionBP := action("break", obj{"function": "main.process"})
	reviewFunctionID := num(asObj(reviewFunctionBP["Breakpoint"])["id"])
	var recoveryHandover string
	if worker {
		recoveryHandover = str(action("handover", obj{"open": false})["handoverId"])
	}
	if e = syscall.Kill(s.PID, syscall.SIGKILL); e != nil {
		t.Fatal(e)
	}
	var occupied net.Listener
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		occupied, e = net.Listen("tcp", s.DAP)
		if e == nil {
			break
		}
	}
	if e != nil {
		t.Fatal("broker did not release listener", e)
	}
	if worker {
		occupied.Close()
		occupied = nil
	} else {
		defer occupied.Close()
	}
	old := s
	cmd = exec.Command(helper, "recover", s.ID)
	if out, e = cmd.CombinedOutput(); e != nil {
		t.Fatalf("recover: %s: %v", out, e)
	}
	s, e = session.Read(s.ID)
	if e != nil {
		t.Fatal(e)
	}
	if s.Token != old.Token || s.HTTP != old.HTTP || s.TargetPID != pid {
		t.Fatal("recovery lost identity or stable HTTP endpoint")
	}
	if !worker && s.DAP == old.DAP {
		t.Fatal("occupied DAP port was not rebound")
	}

	action("clear", obj{"breakpoint": reviewFunctionID})
	reviewActual, reviewErr := delve.Call(s.RPC, "ListBreakpoints", obj{"All": false}, 5*time.Second)
	if reviewErr != nil {
		t.Fatal(reviewErr)
	}
	for _, raw := range asList(reviewActual["Breakpoints"]) {
		if num(asObj(raw)["id"]) == reviewFunctionID {
			t.Fatalf("cleared recovered function breakpoint remains in Delve: %v", raw)
		}
	}
	recovered := state()
	if worker {
		if str(recovered["owner"]) != "vscode" || str(recovered["handoverId"]) == recoveryHandover {
			t.Fatal("recovery lost VS Code ownership or did not refresh the attach request")
		}
		action("reclaim", nil)
	}
	check(recovered, stepLine, "42")
	if asObj(asObj(recovered["state"])["currentThread"])["pc"] != pc || len(asList(recovered["watches"])) != 1 {
		t.Fatal("recovery changed stop or lost watches")
	}
	if _, e = api(s, "POST", "/api/action", obj{"action": "next", "generation": returned["generation"]}); e == nil {
		t.Fatal("pre-crash action was accepted")
	}
	// The same source-level Zed breakpoint must still work under RPC control.
	action("continue", nil)
	atZedBreakpoint := waitPause()
	check(atZedBreakpoint, returnLine, "42")
	action("handover", obj{"open": false})
	d2 := newTestDAP(t, s)
	d2.ok("configurationDone", obj{})
	if event := d2.event("stopped"); num(event["threadId"]) != gid {
		t.Fatalf("reattach focused wrong goroutine: want %d, event=%v", gid, event)
	}
	check(waitPause(), returnLine, "42")
	// Even a frontend asking to terminate on disconnect cannot kill this owned session.
	d2.ok("disconnect", obj{"terminateDebuggee": true})
	d2.c.Close()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !truth(state()["editorConnected"]) {
			break
		}
	}
	action("reclaim", nil)
	check(state(), returnLine, "42")
	after, _ := os.ReadFile(binary)
	if sha256.Sum256(after) != digest {
		t.Fatal("target binary changed during session")
	}
	for _, bp := range asList(state()["breakpoints"]) {
		action("clear", obj{"breakpoint": asObj(bp)["id"]})
	}
	action("continue", nil)
	exited := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if str(state()["status"]) == "exited" {
			exited = true
			break
		}
	}
	if !exited {
		t.Fatal("natural program exit did not settle")
	}
	action("stop", nil)
	if zed.HasProfile(s) {
		t.Fatal("stop left the generated profile behind")
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && session.ProcessExists(s.DelvePID); time.Sleep(30 * time.Millisecond) {
	}
	if session.ProcessExists(s.DelvePID) {
		t.Fatal("stop left a recovered Delve process alive")
	}
	events, err := session.ReadHistory(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, event := range events {
		seen[event.Type] = true
		var payload obj
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if ref := str(payload["snapshot"]); ref != "" {
			dir, err := session.FindHistory(s.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(dir, ref)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, kind := range []string{"session.started", "broker.connected", "execution.stopped", "inspection.captured", "inspection.evaluated", "breakpoint.changed", "session.ended"} {
		if !seen[kind] {
			t.Errorf("missing history event %s", kind)
		}
	}
	t.Logf("PASS: PID %d retained across RPC → DAP → RPC → DAP, total 21 → 42, both breakpoint sets preserved, binary unchanged", pid)
}

func asObj(v any) obj { o, _ := v.(map[string]any); return o }

func asList(v any) []any { a, _ := v.([]any); return a }

func num(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func truth(v any) bool { b, _ := v.(bool); return b }

func pretty(v any) string { data, _ := json.MarshalIndent(v, "", "  "); return string(data) }

func TestIntegrationRunningRecovery(t *testing.T) {
	if os.Getenv("DH_INTEGRATION") != "1" {
		t.Skip("real Delve")
	}
	dir := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(dir, "sessions"))
	t.Setenv("AGENTDEBUGGER_DATA_DIR", filepath.Join(dir, "history"))
	t.Setenv("CODEX_THREAD_ID", "")
	helper := filepath.Join(dir, "agentdebugger")
	binary := filepath.Join(dir, "demo")
	file := filepath.Join(dir, "main.go")
	source := "package main\nimport (\"time\";\"fmt\")\nfunc main(){\ntime.Sleep(4*time.Second)\nfmt.Println(42)\n}\n"
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*exec.Cmd{exec.Command("go", "build", "-race", "-o", helper, "../../cmd/agentdebugger"), exec.Command("go", "build", "-gcflags=all=-N -l", "-o", binary, file)} {
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("%s: %v", out, e)
		}
	}
	run := func(args ...string) obj {
		t.Helper()
		out, e := exec.Command(helper, args...).CombinedOutput()
		if e != nil {
			t.Fatalf("%v: %s: %v", args, out, e)
		}
		v := obj{}
		if e = json.Unmarshal(out, &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	id := str(run("start", "--legacy", "--no-ui", "--binary", binary, "--project", dir)["id"])
	t.Cleanup(func() {
		s, e := session.Read(id)
		if e == nil {
			_ = syscall.Kill(s.PID, syscall.SIGTERM)
			_ = syscall.Kill(s.DelvePID, syscall.SIGTERM)
			time.Sleep(200 * time.Millisecond)
		}
	})
	run("break", id, "--file", file, "--line", "5")
	grant := run("task-authorize", id, "--human", "--instruction", "Run to the next breakpoint")
	taskID := str(asObj(grant["task"])["id"])
	run("continue", id, "--task", taskID)
	before, e := session.Read(id)
	if e != nil {
		t.Fatal(e)
	}
	_ = syscall.Kill(before.PID, syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	run("recover", id)
	state := run("state", id, "--summary")
	if str(state["status"]) != "running" {
		t.Fatalf("recovery fabricated a stop: %v", state)
	}
	fresh := run("state", id)
	if str(asObj(fresh["task"])["status"]) != "cancelled" {
		t.Fatal("recovery retained execution grant")
	}
	if output, err := exec.Command(helper, "next", id, "--task", taskID).CombinedOutput(); err == nil {
		t.Fatalf("recovered broker accepted old task: %s", output)
	}
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		state = run("state", id, "--summary")
		if str(state["status"]) == "paused" {
			break
		}
	}
	if str(state["status"]) != "paused" || num(asObj(state["state"])["Pid"]) != before.TargetPID {
		t.Fatalf("lost running session: %v", state)
	}
	run("end-session", id, "--confirmed")
}
