# Local protocol v2

The broker is one process per debugging session. Delve and the target are separate
processes and survive a client disconnect. The broker exclusively arbitrates agent,
browser, and editor execution. The CLI is a thin JSON frontend; no MCP or gRPC is used.

## Transport and discovery

`start` returns a session ID and inspector URL. `sessions` discovers descriptors in
`DEBUG_HANDOVER_HOME` or the platform cache under `debug-handover/sessions`.
`session.json` has `version: 2`, loopback HTTP/DAP addresses, process identities,
current binding, and durable event state. Clients must reject versions above 2.
A missing version denotes a legacy descriptor; its existing broker stays untouched.
Recovery explicitly upgrades a descriptor to v2 without relaunching its target.

HTTP is bound to `127.0.0.1`. There is no bearer authentication in v2. Other local
processes can access the debugger. Host and browser Origin validation are retained;
POST requires `Content-Type: application/json`. Do not expose this API remotely.
Bindings are coordination identities, not security credentials.

## Commands and state

`GET /api/state?brief=1` returns owner, generation, binding, cursor, status,
notification and capability flags. Omit `brief` to read stack, locals, breakpoints,
source, and watches. `goroutine` and `frame` select inspection scope.

`POST /api/action` accepts `action`, the fresh `generation`, and action arguments.
Agent mutations include `actor: "agent"` and the current `binding` ID. Browser and
editor frontends send their explicit actor (`browser`, `vscode`, or `zed`). These
labels are not access credentials. Stale generations and wrong bindings fail before
execution. Only the owner can step, resume, pause, change breakpoints/watches, or
stop. Read-only evaluation is available to observers at a settled pause.

Operations: `break`, `clear`, `continue`, `next`, `step`, `stepout`, `pause`, `eval`,
`watch`, `unwatch`, `handover`, `reclaim`, `bind`, `event-status`,
`retry-notification`, and `stop`. The CLI documents their flags with `--help`.

`handover` selects `editor: browser|vscode|zed` and optional `note`. `reclaim`
returns a settled pause to the bound agent and always emits `control_returned`.
An explicit browser takeover is allowed while the agent is paused. Editor handback
waits for pending DAP operations. Neither transition resumes or recompiles anything.
`bind` accepts an opaque client ID and display name, increments the binding revision,
and invalidates previous notification routing. Rebinding requires agent ownership.

Errors are JSON `{ "error": "..." }`: 400 invalid event cursor; 403 Host/Origin;
404 unknown route; 415 unsupported content type; 409 stale state, unavailable
operation, incompatible session or expired event cursor. Never retry a failed
execution mutation without reading fresh state and reassessing intent.

## Event stream

`GET /api/events?cursor=N&binding=ID` is SSE. The optional `Last-Event-ID` header
supplies the cursor when the query is absent. Events after that cursor are replayed,
then the connection waits for new events. Heartbeat comments keep the stream alive;
there is no debugger-state polling in this transport.

Each event has monotonic numeric `id`, `kind`, `owner`, `binding` (ID, revision,
display name), optional `note`, and UTC `created`. Kinds include `ownership_changed`,
`control_returned`, `binding_changed`, `stopped`, `target_exited`, and `terminated`.
The latest 256 events and session state are atomically persisted together. On a
409 expired cursor, read state, reconcile its current notification, and reconnect
at its cursor. A slow client must use the same reconciliation after falling behind.

`events ID --cursor N --binding ID` emits JSONL and reconnects after transport
failures. `await-control ID --cursor N --timeout 20s` waits for handback, exit, or
binding change and returns JSON containing the event and fresh state. Timeout
returns a cursor without changing ownership. The maximum wait is one minute.
The harness must keep a tool call active to use this route for automatic continuation.

## Notification delivery

A background listener can wake an idle harness through its supported incoming-message
mechanism. Core events contain no Codex thread ID or Pi conversation internals.
Codex routing is stored separately in `bridge*.json`; Pi owns its binding lifecycle.

Claim delivery with `event-status` (`status: sending`), the event ID, binding ID and
binding revision. Only one pending claim succeeds. Finish as `queued`, `failed`, or
`unknown`. The agent records `acknowledged` after checking current owner/binding/event
and inspecting fresh state. Queued means accepted, not processed. A crash during
sending is ambiguous: do not automatically resend. `retry-notification` creates a
new event only for a current failed/unknown handback. Delivery is not exactly-once.

Harness notifications instruct the agent to verify ownership and destination and
never infer authorization to resume from an event. Forked conversations do not
inherit bindings. Closed harnesses reconcile pending handback on reconnect.
