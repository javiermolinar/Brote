---
name: debug-handover
description: Debug precompiled Go programs with Delve and pass the same paused process between an agent, the browser inspector, Zed, and VS Code. Use for breakpoints, fresh stack and locals inspection, and human-agent debugger handback.
---

Use the shared JSON CLI. In the Codex package, resolve `../../scripts/debug-handover`
from this skill directory (release packages include a prebuilt executable; the source
checkout wrapper builds only the helper). Otherwise use `delve-llm-adapter` on PATH
or the absolute CLI path returned by Pi's `debug_connect` tool. Requires a compatible
installed Delve. Never compile the target implicitly.

## Session workflow

Inspect `sessions` before creating a duplicate. Read `state ID --summary` to confirm the
project, target, owner, binding and pause. Use `recover ID` for an offline broker;
recovery reconnects to the same Delve/target and never relaunches it.

```
delve-llm-adapter start --binary /absolute/debug-binary --project /absolute/project -- [arguments]
delve-llm-adapter break ID --file path.go --line 42 --condition 'attempt == 3'
delve-llm-adapter continue ID --wait 20s --summary
```

Use precompiled executables or test binaries. Test arguments go after `--`, such as
`-test.run TestName`. Changed source does not change the running binary. Verify the
actual breakpoint location and values before reporting that a condition was reached.
A wait timeout does not pause the target.

## Bind the conversation

Codex start uses `CODEX_THREAD_ID` when available and starts a separate notification
listener. `--thread UUID` selects a conversation explicitly; `--thread ""` disables
automatic Codex delivery. `bind ID --thread UUID` changes the destination explicitly.

In Pi, start using `--thread ""`, then call `debug_connect` with the debugger session
ID. That small tool binds this Pi conversation and starts handback listening; use the
shared CLI for all debugger operations. It also returns the executable path. Do not
bind an existing session to a different conversation without the user's intent.

## Human handover

```
delve-llm-adapter handover ID --editor browser --note 'Inspect total before proceeding'
```

Open the returned inspector URL. Browser is included; VS Code and Zed are optional.
`--editor vscode` uses the companion extension. `--editor zed` creates a profile;
press F4 and select the session profile. Both attach to the same paused process.
Closing a frontend never terminates the target. While the human owns execution,
inspect if useful but do not step or resume. Handover requires a settled pause.

A human return emits an event to the connected listener. On notification, read fresh
state and compare owner (`agent`), binding ID/revision, and notification event ID.
Ignore obsolete events. Read the fresh stack and locals, then acknowledge:

```
delve-llm-adapter event-status ID --event EVENT --revision REVISION --status acknowledged
```

Continue the debugging discussion. A notification never authorizes resuming.
Without a notification integration, `await-control ID --cursor N --timeout 20s`
keeps a tool call pending. Timeout leaves ownership unchanged; wait again or explain
that the user should resume the conversation. Do not promise idle-harness wakeup
without a connected notification integration.

## Inspection and lifecycle

`state ID --summary --goroutine N --frame N` selects fresh frames. `eval ID --expression EXPR`
is read-only and rejects arbitrary calls, assignment and channel receives.
`watch`/`unwatch` change saved watches while owning execution. Use `next`, `step`,
`stepout`, `continue`, and `pause` only within the user's debugging authorization.

`stop ID` explicitly ends the owned debug session. Do not stop, restart, or rebuild
as part of handover. `cleanup ID` removes leftover profiles only after processes end.
Failed or uncertain handback delivery is visible in state; check the conversation
before `retry-notification ID`, since delivery may already have succeeded.

## Keep the conversation focused

Use `--summary` with `state` and execution commands using `--wait`. It includes
ownership, event routing, stack, and selected-frame values without source listings
or unrelated goroutine details. Omit it only when those full details are needed.
`--wait` already returns the fresh stopped snapshot; do not immediately fetch it again.
Report the stop location, relevant values or changes, and the next useful debugging
observation. Keep session IDs, binding revisions, event acknowledgements, and setup
mechanics out of normal replies. Surface them when diagnosing a delivery problem.

## Debugger comment questions

A comment notification is a read-only question, not a control handover or a new
implementation task. Read `comment list SESSION` for persisted threads and their
captured stack, locals, source, and timestamps. Check the thread is unresolved,
its delivery question ID matches the event, and its binding ID/revision still
matches `state SESSION`. Ignore obsolete questions. Distinguish captured values
from current execution; do not reclaim or resume to answer a comment.

Before investigating, acknowledge receipt so the debugger shows that you are thinking:

```
delve-llm-adapter comment delivery SESSION THREAD --question QUESTION --binding BINDING --revision REVISION --status thinking
```

Write the answer into a UTF-8 file and post it to the debugger:

```
delve-llm-adapter comment reply SESSION THREAD --question QUESTION --binding BINDING --revision REVISION --message-id QUESTION-answer --body-file /absolute/answer.md
```

Reuse the same message ID on retries; replies are idempotent. The broker rejects
stale questions and changed bindings. Reply in the thread instead of only in the
agent chat. If more execution is needed, explain what to inspect next and let the
user drive or explicitly hand over control. `comment list` also works after the
runtime session ends; those contexts remain historical.
