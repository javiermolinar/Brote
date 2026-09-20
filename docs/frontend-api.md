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
