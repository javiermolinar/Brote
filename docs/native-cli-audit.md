# Native VS Code CLI integration audit

> Historical proposal, superseded by [the shared service architecture](shared-service.md).
> The reuse inventory remains useful; the extension-hosted controller and sequence below are not the implementation plan.

The existing CLI already puts Brote between an agent and Delve. The native
VS Code extension is a separate execution path: it does not register sessions
with that CLI or expose a local API. Adding a transport alone would not connect
these two controllers correctly.

## Existing pieces and reuse boundaries

| Capability | Existing source | Native integration |
| --- | --- | --- |
| JSON CLI commands | `internal/cli/commands.go` | Keep the CLI entry point and familiar inspection/execution verbs; dispatch by backend and advertised capabilities. Tracepoint CRUD needs new commands. |
| Local request transport | `internal/cli/client.go`, `packages/client/src/index.ts` | Reuse HTTP/JSON conventions and timeout/no-redirect behavior. Validate native endpoints and authenticate the new bridge; do not assume legacy tokens are enforced by every old server. |
| Session discovery | `internal/session/session.go`, `internal/session/list.go` | Reuse descriptor discovery and read-only health checks. Add an explicit native backend, editor session identity, workspace identity, and capabilities. Existing discovery probes broker descriptors only. |
| Inspection | `internal/broker/inspection.go`, `packages/vscode/src/native.ts` | Use native capture for native sessions. Old inspection translates Delve state; it cannot inspect an F5 session without a new connection. |
| Execution coordination | `internal/broker/actions.go`, `internal/broker/coordination.go` | Port generation checks, current client bindings, bounded execution scopes, cancellation, and human-pause priority. The implementation is coupled to Go broker locks and Delve state. |
| Event delivery | `internal/broker/events.go`, `internal/cli/events.go` | Reuse cursor/reconcile semantics. Native events must come from the extension's session state and capture outcomes. |
| Harness notifications | `internal/cli/bridge.go`, `internal/cli/tasks.go` | Optional later reuse. The existing `bridge` is a Codex notification listener, not a CLI-to-VS-Code transport. |
| Tracepoint configuration and execution | `packages/vscode/src/tracepoints.ts` | Extract its CRUD operations into an exported controller used by UI, VS Code tools, and the local bridge. Today only the tracker is returned; CRUD is closed over by UI/tool handlers. |
| OTLP correlation | `packages/vscode/src/telemetry.ts`, `packages/vscode/src/extension.ts` | Expose session trace IDs and capture outcomes through state/events. IDs are currently printed in the output channel; tracepoint list results omit them. |

The current checkout includes the standalone broker but no standalone
`capture_execution.go` or `program_trace.go`. The historical tasks describe that
prototype, but those files were not found in the current local branches or the
searched GitHub/worktree directories. The former `agentdebugger` checkout path
is absent and its `delve-llm-adapter` symlink is broken. Do not treat the earlier
OTLP prototype as code available for direct reuse without recovering it first.

## Controller boundary

```text
External agent -> Brote CLI -> local extension API --+
VS Code agent tool --------------------------------+-> native Brote controller
Tracepoints panel ---------------------------------+       |
                                                           +-> VS Code debug session -> Delve
                                                           +-> OTLP exporter -> Tempo
```

VS Code remains the owner of its debugger session. The native bridge must not
invoke the old broker's launch, recover, handover, remote attach, or raw RPC paths.
There is one execution coordinator per native debug session. CLI clients submit
semantic operations rather than arbitrary DAP commands.

Tracepoint definitions currently belong to the workspace, while capture limits
and execution guards belong to a session. Preserve that distinction in the API:
configuration responses identify workspace scope; execution and capture results
identify a specific session. Make multiple VS Code windows and simultaneous
sessions explicit rather than silently selecting the active editor session.

## Implementation sequence

1. Extract the native controller API; retain the same implementation behind the
   UI and language-model tools. Add per-session state and capture outcomes.
2. Publish native discovery records and a private local endpoint with lifecycle
   cleanup, explicit capabilities, bounded requests, and session identity checks.
   Keep the new contract distinguishable from the legacy protocol v2 broker.
3. Route CLI discovery, inspection, and tracepoint CRUD to native sessions. Return
   configuration revision, capture counts/errors, and program/debugger trace IDs.
4. Port cooperative execution coordination before exposing continue/step/pause.
   Reject stale commands; a human action invalidates pending agent execution.
   Share this coordination with tracepoint continuation instead of running two
   independent execution loops.
5. Add native event streaming and CLI-driven integration tests. Optional harness
   wakeups can follow; they are not required for a CLI agent to operate Brote.

Acceptance should cover two sessions/windows, source edits, stale revisions,
human pause during capture, delayed continue responses, client disconnect,
extension reload/termination, unavailable OTLP, and snapshot limits. The final
workflow should use the actual CLI to discover a real F5 session, add captures,
run under an explicit debugging scope, retrieve trace IDs, inspect Tempo, refine
captures, and leave an ordinary breakpoint paused.

## Validation

The existing CLI/session/broker/backend tests are the reuse baseline, especially
session-identity mismatch, task expiry, cancellation before dispatch, and human
pause with a stale generation. These tests do not establish native bridge support.
The tracepoint worktree separately has a real VS Code/Delve/Tempo test, but that
test currently configures tracepoints through a VS Code tool, not an external CLI.
