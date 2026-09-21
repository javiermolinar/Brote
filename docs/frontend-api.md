# Frontend contract (protocol v2)

`packages/client` is the shared TypeScript interface for the standalone web and
native VS Code packages. Its models preserve the current v2 wire format while
backend transport changes independently. Unsupported future protocol versions
fail explicitly. Requests use loopback HTTP, reject redirects, and have bounded
timeouts; optional bearer tokens are only for existing v1 installations.

- `GET /api/state`: session identity, generation, status, binding, capabilities,
  selected frame, stack, variables, breakpoints and watches. `brief=1` omits
  expensive inspection. `goroutine` and `frame` select inspection context.
- `POST /api/action`: existing debugger commands. Execution mutations carry the
  current generation and actor/binding. Stale generations are rejected.
- `GET /api/sources[?file=PATH]`: files present in the debug binary or source text.
- `GET /api/comments`: durable discussion threads and captured historical context.
- `POST /api/comments`: create, ask, reply, delivery, resolve, reopen, retry.
  Replies and acknowledgements identify the thread, question and binding revision.
- `GET /api/events?cursor=N`: SSE journal. Retain the last event ID, deduplicate
  delivery, and reconcile state/discussions after a reset or reconnect.
- `GET /api/sessions` and `POST /api/sessions/stop`: registry and explicit termination.

`createClient().request` is shared by both frontends. `subscribe` uses native
browser EventSource reconnection; `readEvents` provides an abortable streaming
reader for Node and browser consumers. Clients reconcile on reset; they must not
infer successful execution from a connection being open.

Shared types live in `packages/client/src/models.ts`; the existing `Arguments`
field may be empty with DAP, which combines parameters and locals in one scope.
Consumers must inspect both fields. Captured discussions retain their original
snapshot and must not be silently refreshed when execution advances.

Execution-task authorization and cancellation replace ownership in a later
milestone. The existing ownership contract remains in effect until then. No new
MCP interface or authentication flow is introduced.

## Execution coordination

`capabilities.executionTasks` advertises shared human controls with explicitly
scoped agent execution. `owner` remains a compatibility field for old attachment
workflows; it no longer grants execution permission. Normal inspection, questions,
breakpoints, and watches are shared. Human debugger commands cancel an active
agent task. Native DAP clients use the same serialized execution path and do not
need an ownership transition to connect.

Actions on `/api/action`:

- `task-authorize`: human actor, fresh `generation`, and `instruction` create an
  `authorized` task. The `task.authorized` SSE event carries its ID in `note`;
  fetch fresh state for the task and instruction. This does not run the target.
- Agent continue/step/pause commands require `task` and the current `binding`, as
  well as a fresh generation. The first execution changes status to `active`.
- `task-heartbeat`: renews the 60-second lease for that task/binding. Every agent
  execution also renews it. Call heartbeat during long investigations.
- `task-complete`: finishes the matching task at a settled pause.
- `task-cancel`: revokes the task and pauses in-flight execution. Human requests
  carrying the matching task ID remain valid if the pause generation changed.

State exposes `task` (ID, instruction, status, reason, binding, expiry) and
`agentConnected` (a bound SSE listener is connected). Questions never create a
task. A human command, rebind, expired lease, or bound listener disconnect lasting
more than five seconds revokes execution. Recovery cancels persisted grants; it
does not resume execution or halt a target that was already running at the crash.
A grant cannot be reused after cancellation, completion, or recovery, even after
fetching a new generation. These are cooperative local protocol rules, not
identity authentication.

The WebUI exposes Authorize debugging and Stop agent, independently of read-only
inline questions. Harness-specific delivery of `task.authorized`, automatic
heartbeats, and task completion messages belong to the adapters/release phase;
existing question/reply routing is unchanged. Until then, a protocol client or
agent using the CLI can act on a human-authorized task:

```sh
agentdebugger task-authorize SESSION --human --instruction 'Investigate the retries'
agentdebugger next SESSION --task TASK_ID
agentdebugger task-heartbeat SESSION --task TASK_ID
agentdebugger task-complete SESSION --task TASK_ID
agentdebugger task-cancel SESSION --human
```

`--human` represents a direct human operation. Harnesses must never use it to
self-authorize. A human can use `next SESSION --human` without an agent task.
