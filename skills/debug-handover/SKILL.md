---
name: debug-handover
description: Use Brote to debug Go programs from existing binaries or VS Code launch configurations, set breakpoints and tracepoints, inspect captured values and saved traces, and answer debugger comments shared with the browser or VS Code.
---

Use the shared JSON CLI. In the Codex release plugin, resolve
`../../scripts/debug-handover` from this skill directory; it selects the bundled
native executable. In Pi, call `debug_sessions` first to get the native CLI path,
even before a run exists; `debug_connect` also returns it. Use that absolute path
for the commands below, since Git installation does not add a global executable. Otherwise use
`brote` on PATH (`agentdebugger` and `delve-llm-adapter` remain aliases). Release packages
include the browser and require no compiler. Delve must be installed separately.
Never compile the target implicitly. New starts use the shared service. Use
`--legacy` only for an explicitly requested old direct Zed/RPC workflow; add
`--backend rpc` for RPC. Running old sessions retain their contract and cannot
use shared-only operations. `brote session restart ID --new-session` preserves its stored launch mode.

When the project has `.vscode/launch.json`, use `brote session configs --project DIR` to
discover its profiles before reconstructing launch arguments. Start a named Go
profile with `brote session start --project DIR --config "Profile name"`. For `debug`,
`test`, or `auto` profiles, add `--build` when building is within the user's
request; `exec` profiles launch their prebuilt binary without that flag.
`--file PATH` supplies an active file for `${file}`/`${fileDirname}` and `auto`
mode. `--launch-file PATH` reads another launch file. Brote imports args, cwd,
env/envFile, build flags and platform overrides; unsupported features such as
task hooks, interactive terminals and command/input variables fail explicitly.
Arguments after `--` append to the profile's arguments. New runs remain paused;
recovery, handover and Run again never rebuild the target.

For reusable launch settings, create or edit `.vscode/launch.json` directly.
Preserve unrelated profiles and comments, use `${workspaceFolder}` for project
paths, and retain `envFile` or `${env:NAME}` references for environment settings.

## Connect and inspect

Check `brote session list --all` before creating a duplicate. Read `brote debug state ID --summary` to confirm
the project, target, binding, task and pause. `brote session recover ID` reconnects an offline
broker to the same debugger/target; it does not relaunch the program.

```sh
brote session start --binary /absolute/debug-binary --project /absolute/project -- [arguments]
brote debug breakpoint add ID --file /absolute/main.go --line 42 --condition 'attempt == 3'
brote debug state ID --summary --goroutine 1 --frame 0
```

Test arguments go after `--`, such as `-test.run TestName`. Changed source does not
change the binary. Check the actual breakpoint location and values before reporting
that a condition was reached. `brote debug eval ID --expression EXPR` reads values; it rejects
arbitrary calls, assignment and channel receives. Breakpoints, watches and inspection
are shared; the legacy `owner` field does not authorize execution.

Codex uses `CODEX_THREAD_ID` on start and a detached event bridge to wake this
conversation. `brote session bind ID --thread UUID` explicitly connects an existing run.
In Pi, start with `--thread ""`, then use `debug_connect` or `/debug-connect ID`.
Do not rebind someone else's conversation merely to inspect it. Both integrations
return the inspector URL; opening it never resumes the program. An offline agent
can reconnect while the debugger remains alive.

## Requested debugging tasks

An explicit request such as “debug this test” or “investigate why this program
retries” authorizes running and stepping within that investigation. Record the
request as a task and proceed; do not ask the user to approve it again in the
inspector. Starting a task leaves the program paused for breakpoint setup.

Attaching, viewing state, questions about captured values, and debugger comment
events never authorize execution. Do not create a task from those requests.
If execution is needed to answer a read-only question, explain what a debugging
investigation would check. Agents use `debug task start`, never `--human` or the legacy
human-only `task-authorize` command.

Read fresh state first and confirm the target and binding belong to the requested
investigation. To record a chat request in Codex:

```sh
brote debug task start ID --binding BINDING --revision REVISION --instruction "Debug the selected test, inspect the retry, then leave it paused"
```

Use the returned task ID. This request is already acknowledged in the active
conversation and does not send a duplicate follow-up. For a request initiated in
the inspector, the user can choose **Start debugging**, which delivers the task
to the connected conversation.

On a task event, read fresh state. Match task ID, binding ID/revision and unexpired
`authorized`/`active` status; ignore obsolete events. Follow only that instruction.

**Pi:** for a chat request, call `debug_task` with `operation: start`, session and
`instruction` containing the user's requested scope. For a delivered inspector
task, use `operation: claim`, session and task. Both associate the active host turn with Go-owned lease renewal. Use `debug_execute` with the
same session/task and operation `continue`, `next`, `step`, `stepout` or `pause`.
Finish with `debug_task` `complete` at a settled pause or exit; use `cancel` on failure.
Pi reports settled/closed facts; Go completes or cancels the matching task.
An idle listener never renews a lease. Do not substitute a bare shell continue for these tools.

**Codex:** after starting a task, use the same CLI task contract. Heartbeat first
when acknowledging a task delivered by the inspector:

```sh
brote debug task heartbeat ID --task TASK --binding BINDING
brote debug task execute ID --task TASK --binding BINDING --operation next --wait 30s
brote debug task complete ID --task TASK --binding BINDING
# On failure or abandonment:
brote debug task cancel ID --task TASK --binding BINDING
```

`brote debug task execute` renews while that bounded tool call runs and cancels/requests a pause
on timeout or interruption. It returns a fresh compact snapshot when stopped.
Between calls, heartbeat before the 60-second lease expires while actively working.
The detached event bridge never renews a task. If the lease expires or the human
pauses or cancels, stop executing; do not start a replacement without a new user
request. Human stepping,
rebind and listener disconnect can revoke the task too. Complete only after the
requested investigation is done, not automatically after every step.

Delivery state distinguishes queued, acknowledged and uncertain requests. A
reconnect reconciles persisted state; it does not replay a queued/ambiguous task.
For uncertain delivery, inspect the conversation before cancelling and starting
a replacement at the user's request. A legacy handback event permits fresh inspection only; acknowledge
with `brote session events acknowledge` after checking event and binding, and require a task for execution.

## Capture values with tracepoints

Use a breakpoint to stop and inspect; use a tracepoint to collect values across
hits during an authorized investigation. Check `brote debug capabilities ID` for
shared-session tracepoint support. Choose an executable source line where the
expressions are in scope; function tracepoints (`--function`) are unsupported.

List existing definitions before adding one. Give temporary investigation points
`--scope run` so they expire on restart; session scope persists across runs.
Keep capture names stable for comparisons and select only the values you need:

```sh
brote debug tracepoint list ID --client BINDING
brote debug tracepoint add ID --client BINDING --file /absolute/main.go --line 42 --name retry --values '{"attempt":"attempt","total":"total"}' --capture-limit 10 --scope run
```

Replace the path, line and expressions with verified locations in the target.
`--values` maps labels to read-only Go expressions. An optional `--condition`
filters hits. The capture limit bounds attempts per run, not execution time.
Configuring a tracepoint never resumes the target. Use the active task's bounded
execution command from above to continue; in Pi use `debug_execute`. Tracepoints
can resume after a successful capture during an authorized continue. Human Pause,
ordinary breakpoints, failed/truncated captures and exhausted limits keep the
target stopped; inspect the outcome before deciding to continue again.

```sh
brote debug captures ID
brote query traces --session ID
brote query traces TRACE_ID
```

Check capture status and values before drawing conclusions; report skipped,
truncated or failed captures. Use the returned program/debugger trace IDs, and
retry a pending trace query after a short wait rather than rerunning the target.
Remove temporary points when finished, using their current definition revision:

```sh
brote debug tracepoint remove ID --client BINDING --id POINT --revision REVISION
```

For updates or removal, preserve the returned owner (`--client`), ID and revision;
re-list on a stale revision. Do not remove another client's points. In Pi, use
`debug_tracepoints` for list/create/update/delete and `debug_captures` for outcomes
and trace IDs; preserve the same owner, revision and scope.

## Debugger comment questions

A comment event is read-only discussion, not a new implementation request. Read
`brote comment list SESSION` for persisted messages and captured stack, locals, source
and timestamps. Verify unresolved status, question ID, and binding ID/revision
against fresh state. Ignore obsolete questions. Captured values are historical;
do not resume to answer a comment.

Acknowledge before investigating:

```sh
brote comment delivery SESSION THREAD --question QUESTION --binding BINDING --revision REVISION --attempt ATTEMPT --status thinking
```

Write the answer to a UTF-8 file, then post it to the debugger:

```sh
brote comment reply SESSION THREAD --question QUESTION --binding BINDING --revision REVISION --attempt ATTEMPT --message-id QUESTION-answer --body-file /absolute/answer.md
```

Reuse the message ID on retry; replies are idempotent. Answer in the debugger
thread, not only agent chat. If execution is needed, explain the next inspection
and let the user drive or request an investigation. `brote comment list` works after the run ends.

## Lifecycle and reporting

`brote session stop ID --confirmed` terminates that debugger and target; use only for an
explicit request to end the named run. Disconnecting an agent or closing a frontend
does not end it. Do not stop, restart or rebuild as part of connecting. `brote session list --all`
includes saved runs; `brote session history ID` reads durable events without a live process.

Prefer `brote debug state ID --summary`; avoid immediately repeating the snapshot returned by
`brote debug task execute`. Report stop location, relevant values and the next observation.
Keep routing IDs and acknowledgements out of normal replies unless diagnosing a
connection or delivery failure.


## Saved discussions

Use the exact question, recipient revision and attempt from the canonical delivery
message. A sender receipt is not recipient acknowledgement or a final answer.
Uncertain sends require inspection and explicit retry; never blindly reinject.
Provider questions have an explicit recipient separate from the execution binding.
Do not silently switch destinations or start execution to answer them.

`comment ask SESSION THREAD --context original --body-file PATH --offline` adds
an original-evidence follow-up after exit. Offline reply/resolve/reopen/retry use
the same Go records and never start Delve. Current evidence requires a live pause.
Saved partial/truncated/error notes must be preserved in the answer's explanation.

## Saved traces

Brote core stores shared-session tracepoint captures and debugger actions in embedded
Tempo. Native editor adapters submit bounded read-only observations to the same
tracing service. `brote query traces` lists saved program/debugger IDs and independent
local/remote export status; `brote query traces TRACE_ID` reads saved Tempo JSON after the
broker exits. Shared records use session:run keys, so restart preserves earlier
traces. An assigned ID or accepted export does not prove query availability or
crash durability. Agents do not launch a separate Tempo process or collector.
