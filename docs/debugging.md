# Debugging guide

Use an existing Go executable or test binary with DWARF symbols. If a build is
needed, do it once following the project's build instructions, commonly with
`-gcflags='all=-N -l'`. Starting or handing over a session never compiles the target.

## Demo

From a source checkout (development only):

```sh
go build -gcflags='all=-N -l' -o /tmp/handover-demo ./examples/demo
scripts/debug-handover start --binary /tmp/handover-demo --project "$PWD" --thread ''
```

Use the returned ID with `break ID --function main.process`, or select a source line
and condition with `break ID --file examples/demo/main.go --line N --condition
'attempt == 3'`. `continue ID --wait 20s` executes to the breakpoint. Inspect fresh
`state ID` before claiming a condition fired. The state contains source, locals,
stack, goroutines, watches, breakpoint conditions, owner, binding, and event cursor.

## Shared controls

`handover ID --editor browser` transfers a settled pause to the embedded inspector.
The browser can step, continue, pause, edit breakpoints/watches, or stop only while
it owns execution. Observers can read variables. The Return button leaves the process
paused, emits `control_returned`, and lets a connected listener notify the agent.

`handover ID --editor vscode` requires the companion installed by setup. It attaches
to the correct trusted project automatically. Use Give Control to Agent in the status
bar to hand back; the extension command identifier remains `debugHandover.reclaim`.
`handover ID --editor zed` creates a JSONC profile without replacing unrelated profiles.
Open F4 and select `Debug Handover · ID`. Zed attachment is manual in this release.

`reclaim ID` explicitly returns the same pause to the agent. Receiving an event never
authorizes resuming. Agents must compare its ID/binding/revision to current state,
read fresh stack and locals, and acknowledge the event.

## Conversation handback

Codex start binds `CODEX_THREAD_ID` unless `--thread ''` is supplied. A separate bridge
listens over SSE and uses the installed `codex queue --thread` capability. Its routing
and cursor are stored separately from core session state. `doctor` checks capability.

Pi uses the shared CLI for debugging. After starting, call `debug_connect` or use
`/debug-connect ID`; this binds the Pi conversation and starts a streaming listener.
On resume it reconnects matching sessions; forks do not inherit control. The extension
must remain loaded for automatic handback. Delivery errors appear in the inspector
and Pi notifications.

`events ID --cursor N` streams JSONL without polling. `await-control ID --cursor N
--timeout 20s` returns to an active tool call on handback or exit. If it times out,
continue waiting from the returned cursor. Without a harness notification mechanism,
a completed agent turn cannot be awakened by the CLI alone.

Notification states are pending, sending, queued, failed, unknown, and acknowledged.
Queued means accepted, not read. A crash during sending is ambiguous. Check the
conversation before `retry-notification ID`; automatic resend could duplicate it.

## Inspection and recovery

- `state ID --goroutine N --frame N`: select fresh inspection scope.
- `eval ID --expression EXPR --depth 3 --count 64`: bounded read-only evaluation.
- `watch ID --expression EXPR` / `unwatch`: saved expressions, up to 16.
- `next`, `step`, `stepout`, `continue`: execution commands with optional `--wait`.
- `pause ID`: halt an owned running target.
- `recover ID`: reconnect a failed broker to the recorded Delve process and target PID.
- `stop ID`: explicitly terminate the owned debug session.
- `cleanup ID`: remove leftover generated profiles after all session processes end.

A broker crash does not destroy target memory while Delve survives. Recovery preserves
ownership, bindings, event history, watches, and breakpoints. If a port was occupied,
use the new panel URL. Event clients reconnect using the refreshed descriptor.
Source fingerprints identify on-disk changes, but cannot prove an arbitrary precompiled
binary matches the source: `unverified` means unknown, not a match.

## Testing

`DH_INTEGRATION=1 CODEX_THREAD_ID='' go test -race ./...` builds temporary debug targets
and tests real Delve JSON-RPC/DAP ownership, browser handback events, stepping, unchanged
PID/memory, frontend disconnect, and hard broker recovery. Notification tests use local
stubs and never send unsolicited messages to real conversations. Node tests cover the
Pi lifecycle and VS Code protocol. The release workflow packages four platform bundles;
cross-compilation alone is not evidence of native runtime validation on each platform.
