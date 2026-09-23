# Embedded trace storage

Brote's Go core owns trace capture, span IDs, export, Tempo lifecycle and queries.
Native VS Code submits DAP requests, responses, stops and bounded observations to
Brote's API. Shared Go brokers submit revision-checked tracepoint captures and debugger actions, so
Codex and Pi use the same capability without an editor. The TypeScript extension
contains no OpenTelemetry provider or Tempo lifecycle implementation.

The first producer or query starts `brote trace-serve` automatically. Concurrent
clients discover one loopback API and an exclusive OS lock prevents duplicate
stores. Tempo runs inside that core service using the unmodified go.mod dependency.
A producer sends a heartbeat every 30 seconds; abandoned captures close after 90
seconds without activity. Once captures are closed and the API has been idle for a
minute, the service drains exports and stops Tempo. Closing VS Code closes its
captures, not another client's runtime. Startup and shutdown have deadlines.

Data lives at `session.DataRoot()/tracing`: on macOS this is
`~/Library/Application Support/AgentDebugger/tracing`; Linux uses
`$XDG_DATA_HOME/agentdebugger/tracing` or `~/.local/share/agentdebugger/tracing`.
`AGENTDEBUGGER_DATA_DIR` overrides the application data root. Trace records live in
`sessions/` and Tempo WAL/blocks in `tempo/`. Data survives application upgrades and
normal service restarts. Prior experimental VS Code workspace stores are not
automatically imported into this shared store.

`brote traces` lists saved IDs and export status; `brote trace TRACE_ID` returns
stored JSON even after a broker ends. Pi exposes `debug_traces`. VS Code's
**Brote: Session Traces** uses the same API to list, copy and query IDs. The shared
HTTP client can call `trace-sessions`, `trace-events` and `traces?id=TRACE_ID` on the
endpoint returned by `brote trace-service`. Assigned IDs, accepted export, and
successful query are separate states. There is no graphical trace viewer yet.

## Export and failure handling

Local capture requires no OTLP environment. For optional remote export, set
`OTEL_EXPORTER_OTLP_ENDPOINT` (base URL) or
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` (complete URL) in the environment that starts
Brote core. The shared service reads configuration at startup; restart it after
changing settings. The protocol is HTTP/protobuf. Trace-specific headers take
precedence over generic headers, and percent-encoded values support Grafana Cloud
Basic authorization. Remote TLS verifies certificates; standard certificate and
client-certificate/key paths are supported. No remote headers or credentials enter
local ingestion, trace records or error messages.

The core makes independent copies for bounded local and remote queues. Each export
has a two-second deadline. Remote failure does not block local ingestion. Failures
remain visible per trace and destination even if a later batch succeeds. Producers
and queues are bounded; overload and transport interruption can lose observations.
Lost events are not replayed automatically. Producers report loss to the core so
incomplete status survives record refresh and restart. A service restart cannot
reconstruct in-flight DAP requests; abandoned open records are marked closed and
incomplete before they are listed. Existing producers can resume an interrupted
capture with a heartbeat or observation, keeping the trace IDs and beginning a
new root span segment. Explicitly closed captures stay closed.

Tempo acknowledges ingestion before periodic WAL flush. Killing the process
immediately after acceptance can lose recent spans. Normal shutdown cuts remaining
traces to WAL. Neither export acceptance nor query availability guarantees survival
of power loss or a full disk.

## Tempo profile and compatibility

Go 1.27.1 or newer is required. Tempo is pinned normally in go.mod with no Tempo
replacement, source patches or preparation step. Worker and scheduler stay enabled;
the worker owns backend polling. The metrics-generator module is skipped, processors
are empty, and stock forwarding machinery receives no generation work.

The core calls the wrapped distributor consumer directly. Queries invoke the stock
querier handler in process with `mode=blocks`, reading persisted backend blocks. Brote requests
protobuf and uses Tempo's v1 JSON marshaller to retain the public `batches` shape.
This route avoids populating the frontend queue that stalls stock shutdown; it does
not repair that upstream queue or introduce a search API. Backend-only mode also
avoids the live-query path associated with reproduced span loss. Shared mutable
batches in the pinned live-store combiner are the suspected cause; an isolated
regression has not yet established that exact mechanism.
New spans become queryable after block flushing and polling (usually within tens of
seconds), not immediately after export. Core records track expected span counts;
queries report pending/incomplete while the stored result has fewer spans. Active
traces can grow after a query; this check does not promise a final snapshot of an
ongoing session.

The current profile cuts blocks every 10 seconds and polls every 2 seconds.
This is a temporary freshness tradeoff: frequent small blocks increase disk and
compaction work. Direct querier use does not require backend-only mode. Safe live
reads and frontend shutdown are separate unresolved concerns; the Go-core ownership
model requires neither workaround.

Tempo's internal gRPC and required OTLP receiver bind two dynamic loopback ports.
The shared Brote API adds a third loopback listener with host/origin checks and JSON
content-type enforcement. There is no local authentication token. Other local
processes can access these listeners. Authentication applies only to remote export.

Stock Tempo has no infinite-retention switch. Brote uses the maximum positive Go
duration (about 292 years) and retains cleanup of replaced compacted blocks. Zero
retention is not infinity. Brote does not delete traces to relieve disk pressure.
The internal query gRPC limit is 128 MiB; this is a retrieval limit. The low-level
runtime suite covers a 700-snapshot trace across actual compaction and restart.

The `embedded-tempo` pipe command remains a low-level compatibility/test entrypoint;
production adapters use the shared core API. Its TypeScript workload generator lives
under test fixtures and is not bundled into the extension.

## Distribution

Release packaging includes the pinned Tempo source, its license and notices, and
Brote build materials. Original Brote source notices remain in place. Use normal Go
build/test commands or `make vsix`. See [current validation](embedded-tempo-validation.md)
for executed checks and limits.

## Shared-session merge

Shared debugger sessions use one broker producer and submit prebuilt spans to
`trace-spans`; native adapters continue using `trace-events`. Both feed the same
local/remote queues and Tempo instance. Shared records are keyed by `session:run`
and retain their trace/capture IDs. No editor producer is installed for Brote DAP
sessions. An older running trace service without shared-span support must be
restarted; it is never silently killed to upgrade it.
