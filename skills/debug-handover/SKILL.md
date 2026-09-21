---
name: debug-handover
description: Use Brote to inspect precompiled Go programs, set breakpoints, answer debugger comments, and execute explicitly authorized debugging tasks shared with the browser or VS Code.
---

Use the shared JSON CLI. In the Codex release plugin, resolve
`../../scripts/debug-handover` from this skill directory; it selects the bundled
native executable. In Pi, `debug_connect` returns the CLI path. Otherwise use
`brote` on PATH (`agentdebugger` and `delve-llm-adapter` remain aliases). Release packages
include the browser and require no compiler. Delve must be installed separately.
Never compile the target implicitly.

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

## Authorized execution tasks

A debugger question or attachment never authorizes execution. The user grants an
execution task through **Authorize debugging** in the inspector (or VS Code's
confirmed execution tool). Agents must not self-authorize or use `--human`.
If no current grant exists, explain the execution needed and where to authorize it.

On a task event, read fresh state. Match task ID, binding ID/revision and unexpired
`authorized`/`active` status; ignore obsolete events. Follow only that instruction.

**Pi:** call `debug_task` with `operation: claim`, session and task. This acknowledges
receipt and renews the lease during the active turn. Use `debug_execute` with the
same session/task and operation `continue`, `next`, `step`, `stepout` or `pause`.
Finish with `debug_task` `complete` at a settled pause; use `cancel` on failure.
Pi also completes/cancels remaining claimed work when the turn settles and cancels
on session shutdown. Do not substitute a bare shell continue for these tools.

**Codex:** use the same CLI task contract:

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
The detached event bridge never renews a grant. If the lease expires or the human
cancels, stop executing; do not silently authorize a replacement. Human stepping,
rebind and listener disconnect can revoke the task too. Complete only after the
requested investigation is done, not automatically after every step.

Delivery state distinguishes queued, acknowledged and uncertain requests. A
reconnect reconciles persisted state; it does not replay a queued/ambiguous task.
For uncertain delivery, inspect the conversation before the user cancels and grants
a replacement. A legacy handback event permits fresh inspection only; acknowledge
with `event-status` after checking event and binding, and require a task for execution.

## Debugger comment questions

A comment event is read-only discussion, not a new implementation request. Read
`comment list SESSION` for persisted messages and captured stack, locals, source
and timestamps. Verify unresolved status, question ID, and binding ID/revision
against fresh state. Ignore obsolete questions. Captured values are historical;
do not resume to answer a comment.

Acknowledge before investigating:

```sh
brote comment delivery SESSION THREAD --question QUESTION --binding BINDING --revision REVISION --status thinking
```

Write the answer to a UTF-8 file, then post it to the debugger:

```sh
brote comment reply SESSION THREAD --question QUESTION --binding BINDING --revision REVISION --message-id QUESTION-answer --body-file /absolute/answer.md
```

Reuse the message ID on retry; replies are idempotent. Answer in the debugger
thread, not only agent chat. If execution is needed, explain the next inspection
and let the user drive or authorize a task. `comment list` works after the run ends.

## Lifecycle and reporting

`end-session ID --confirmed` terminates that debugger and target; use only for an
explicit request to end the named run. Disconnecting an agent or closing a frontend
does not end it. Do not stop, restart or rebuild as part of connecting. `history`
lists saved runs; `history ID` reads durable events without a live process.

Prefer `state --summary`; avoid immediately repeating the snapshot returned by
`task-execute`. Report stop location, relevant values and the next observation.
Keep routing IDs and acknowledgements out of normal replies unless diagnosing a
connection or delivery failure.
