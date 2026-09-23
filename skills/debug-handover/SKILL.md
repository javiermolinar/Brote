---
name: debug-handover
description: Use Brote to debug Go programs from existing binaries or VS Code launch configurations, set breakpoints, answer read-only debugger comments, and investigate user-requested debugging tasks shared with the browser or VS Code.
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
use shared-only operations. `run-again` preserves its stored launch mode.

When the project has `.vscode/launch.json`, use `brote configs --project DIR` to
discover its profiles before reconstructing launch arguments. Start a named Go
profile with `brote start --project DIR --config "Profile name"`. For `debug`,
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

Check `sessions` before creating a duplicate. Read `state ID --summary` to confirm
the project, target, binding, task and pause. `recover ID` reconnects an offline
broker to the same debugger/target; it does not relaunch the program.

```sh
brote start --binary /absolute/debug-binary --project /absolute/project -- [arguments]
brote break ID --file /absolute/main.go --line 42 --condition 'attempt == 3'
brote state ID --summary --goroutine 1 --frame 0
```

Test arguments go after `--`, such as `-test.run TestName`. Changed source does not
change the binary. Check the actual breakpoint location and values before reporting
that a condition was reached. `eval ID --expression EXPR` reads values; it rejects
arbitrary calls, assignment and channel receives. Breakpoints, watches and inspection
are shared; the legacy `owner` field does not authorize execution.

Codex uses `CODEX_THREAD_ID` on start and a detached event bridge to wake this
conversation. `bind ID --thread UUID` explicitly connects an existing run.
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
investigation would check. Agents use `task-start`, never `--human` or the legacy
human-only `task-authorize` command.

Read fresh state first and confirm the target and binding belong to the requested
investigation. To record a chat request in Codex:

```sh
brote task-start ID --binding BINDING --revision REVISION --instruction "Debug the selected test, inspect the retry, then leave it paused"
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
brote task-heartbeat ID --task TASK --binding BINDING
brote task-execute ID --task TASK --binding BINDING --operation next --wait 30s
brote task-complete ID --task TASK --binding BINDING
# On failure or abandonment:
brote task-cancel ID --task TASK --binding BINDING
```

`task-execute` renews while that bounded tool call runs and cancels/requests a pause
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
with `event-status` after checking event and binding, and require a task for execution.

## Debugger comment questions

A comment event is read-only discussion, not a new implementation request. Read
`comment list SESSION` for persisted messages and captured stack, locals, source
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
and let the user drive or request an investigation. `comment list` works after the run ends.

## Lifecycle and reporting

`end-session ID --confirmed` terminates that debugger and target; use only for an
explicit request to end the named run. Disconnecting an agent or closing a frontend
does not end it. Do not stop, restart or rebuild as part of connecting. `history`
lists saved runs; `history ID` reads durable events without a live process.

Prefer `state --summary`; avoid immediately repeating the snapshot returned by
`task-execute`. Report stop location, relevant values and the next observation.
Keep routing IDs and acknowledgements out of normal replies unless diagnosing a
connection or delivery failure.


## Saved discussions and Pi captures

Use the exact question, recipient revision and attempt from the canonical delivery
message. A sender receipt is not recipient acknowledgement or a final answer.
Uncertain sends require inspection and explicit retry; never blindly reinject.
Provider questions have an explicit recipient separate from the execution binding.
Do not silently switch destinations or start execution to answer them.

`comment ask SESSION THREAD --context original --body-file PATH --offline` adds
an original-evidence follow-up after exit. Offline reply/resolve/reopen/retry use
the same Go records and never start Delve. Current evidence requires a live pause.
Saved partial/truncated/error notes must be preserved in the answer's explanation.

In Pi, use `debug_tracepoints` for list/create/update/delete, preserving the
returned owner/ID/revision and scope. Use `debug_captures` for bounded outcomes,
export status and program/debugger trace IDs. These tools do not resume execution;
use an authorized `debug_task` and `debug_execute` for that.

## Saved traces

Brote core stores shared-session tracepoint captures and debugger actions in embedded
Tempo. Native editor adapters submit bounded read-only observations to the same
tracing service. `brote traces` lists saved program/debugger IDs and independent
local/remote export status; `brote trace TRACE_ID` reads saved Tempo JSON after the
broker exits. Shared records use session:run keys, so restart preserves earlier
traces. An assigned ID or accepted export does not prove query availability or
crash durability. Agents do not launch a separate Tempo process or collector.
