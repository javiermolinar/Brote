# Runtime ownership after consumer migration

| Concern | Authoritative owner | Host responsibility |
| --- | --- | --- |
| Debugger connection, run/pause identity, inspection | Persistent Go broker | CLI transport; VS Code DAP/frame selection; browser rendering |
| Breakpoints, tracepoints, hit attribution, capture/continuation | Go service | Definition UI/tools with owner/revision; no second debugger controller |
| Span generation and trace association | Go session broker for shared sessions; Go tracing core for native adapters | Native adapters report bounded read-only DAP observations; Brote sessions never duplicate them |
| Local Tempo, OTLP export and saved trace queries | Shared Go tracing service | Display destination status; query via `traces`/`trace` |
| Task grant, lease renewal, cancellation | Go broker | Pi reports challenged active/idle/closed turn facts; Codex uses bounded task commands |
| Durable delivery claims, attempts, cursor reconciliation | Common Go ledger and managed CLI stream | Pi injects canonical text; Codex invokes queue; hosts report send outcomes |
| Question/reply association, immutable evidence, import/offline writes | Locked Go discussion store and common reducer | VS Code invokes models and renders transient drafts/streams; browser HTTP/SSE is a facade |
| Provider cancellation | Host reports the interrupted outcome through CLI | VS Code awaits its own in-flight writes on normal shutdown; no global sweep of other windows |

New `start` launches default to shared service. `--legacy`/`--service=false` opt out;
`--backend rpc` requires that opt-out. Existing legacy sessions remain unchanged,
and `run-again` preserves the stored launch mode. Direct Zed TCP/RPC handover is a
legacy-only compatibility workflow. Shared sessions use authenticated `brote dap`.

`legacy_bridge.go` and `legacy_comment_delivery.go` retain old delivery builders
and cursor handling solely for `ServiceVersion == 0`. Shared bridges never fall
back to those handlers; missing coordination capability fails explicitly. The old
CLI names remain executable aliases. These shims preserve existing sessions, not
a second shared policy implementation.

Native VS Code workspace data is retained as an import backup/receipt and stable
tracepoint ID mapping. Go definitions take precedence over legacy metadata. No
TypeScript code owns authoritative conversation, continuation, exporter or lease
state. Browser rendering cursors and transient drafts are presentation state.

Normal VS Code reload persists provider interruption; abrupt OS death cannot run
that callback and requires inspection and explicit retry. Codex queue acceptance
is only queued delivery, never proof of an active agent turn. No integration
claims exactly-once external delivery; ambiguous sends remain unknown.

The tracing service is a separate lazy process shared across debugger sessions.
It embeds Tempo and owns both local ingestion and optional remote export. Shared
session brokers send bounded span batches with their existing run/capture IDs;
native editor adapters send observations. Shared trace records use session:run keys
so restart cannot overwrite earlier trace IDs. Session HTTP/DAP authentication is
separate from the loopback tracing API, which has host/origin checks but no token.
