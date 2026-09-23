# Brote core traces

Brote Go core stores debugger and program traces from VS Code, Codex and Pi using embedded Tempo
runtime. No endpoint or separately launched backend is required. Use **Brote: Session
Traces** to copy either trace ID or open its stored JSON. Local data survives normal
shutdown and upgrades. Retention is currently configured to about 292 years.
See [storage details](embedded-tempo.md) for the finite retention and loopback listener limits.

To additionally export to a remote backend, set the environment before launching
Brote (or VS Code when it starts the core, including remote extension hosts):

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
# Optional, percent-encoded header values:
# export OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer%20TOKEN'
code .
```

The shared core reads its environment at startup. Restart an existing core after changing
settings; a new VS Code window may also reuse the old editor environment. These settings configure optional remote export; local storage
remains enabled with its own exporter queue.

The generic endpoint receives `/v1/traces`; a traces-specific
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` supplies the full URL and works on its own. Traces-specific headers
override generic headers. Only `http/protobuf` is supported. Invalid configuration
disables remote export and records a credential-free failure status in the core trace record.
Authentication belongs in headers, not URL credentials. Export is asynchronous and
best effort; an unavailable backend does not block debugging.

Each native debug session produces two traces:

- **Debugger:** a `debugger.session` root and DAP command spans. Execution spans
  wait for both a successful response and a stop/exit event, in either order.
  Ordinary stack/scope/variable refresh requests are omitted. Request bodies,
  expressions, and free-form adapter errors are not exported here.
- **Program:** a run root, thread observation spans, and snapshots from breakpoint
  stops (source, function, data, or instruction breakpoints) or explicit inspections. Snapshots contain the selected/reported thread's
  stack and bounded non-expensive scopes. They do not resume execution.

Both include `debugger.session.id`. Program spans use the debug profile name as
`service.name`; keep it stable between runs. The program root links to the debugger
root. The core returns both trace IDs through its API and saves them in its application data directory. Parents finish when VS Code
stops the session, the adapter terminates/exits, or the extension shuts down; children may arrive first.

## Comparing runs

Use stable function names and optional labels in VS Code settings:

```json
{
  "brote.capturePoints": {
    "main.process": {
      "name": "process.result",
      "values": { "total": "total" }
    }
  }
}
```

Keys match exact frame names supplied by the adapter. This configuration labels
existing captures; set breakpoints using VS Code. `values` selects already captured
locals by name, without expression evaluation. Exact debugger strings become
`program.value.<alias>`. Companion status attributes distinguish `available`,
`ambiguous`, `not_captured`, `truncated`, and `non_scalar`. Aliases use lowercase
letters, digits, and underscores, beginning with a letter (maximum 64 characters).
At most 16 selections are exported. Names must be at most 128 characters.

Thread names describe the first observed stack, without numeric IDs or source line
numbers. Generic DAP does not expose goroutine creation sites, binary hashes, or
spawn relationships. Repeated workers can still share names; stable names do not
solve ambiguous concurrent-worker pairing in a diff.

Native program payloads use `program.schema.version=2`, `program.thread.id`, and
`program.span.type` values `run`, `thread`, and `snapshot`. They differ from the
prototype's Delve-specific goroutine schema. A thread span covers an observation
window, not function duration, CPU time, or a full goroutine lifetime. Snapshot
spans have zero duration; debugger pauses distort wall-clock timings.

Exported span names and service names are limited to 512 UTF-8 bytes; adapter
types are limited to 128 bytes. Truncation preserves whole Unicode characters.

Captures read at most 30 frames, 8 scopes, and 50 variables per scope, with values
limited to 2,000 characters. Capture requests share a two-second deadline; DAP
cannot guarantee cancellation of adapter work already issued. Moving the debugger
discards mixed-state captures. Automatic collection allows one pending capture per
session. Snapshots are reduced to valid JSON under 32 KiB; Brote configures Tempo's
attribute limit to 65,536 bytes. Each capture tracks at most 256 threads and 1,000
snapshots; the extension tracks 16 simultaneous sessions. The adapter and Go broker
buffer at most 256 observations while core startup completes. Core export queues
hold 64 batches per destination and export calls have two-second deadlines. A final
close event is retained separately from the adapter's normal backlog limit.
Local listeners bind to loopback; authentication headers configure only remote export.
Crashes, full queues, and interrupted shutdown can lose data.

The native adapter does not add automatic continue tracepoints, expression capture,
or Delve-specific origin lookup. Broker sessions support the existing Go recovery
and inspection workflows independently of the native adapter.

## Verification

```sh
go test -race ./...
npm run build && npm test
go build -o bin/brote ./cmd/brote
BROTE_CORE_INTEGRATION=1 node --test packages/client/test/core-tracing.test.cjs
```

The opt-in core suite starts Brote's embedded backend in isolated data directories
and covers concurrent clients, native observations, real Delve broker capture,
remote failure and service restart. CI checks Brote's integration boundary;
Tempo's compaction and storage durability belong to Tempo's upstream test suite.

### Real VS Code host

With Go, Delve, VS Code, and the Go extension installed (Tempo starts automatically):

```sh
BROTE_GO_EXTENSION=/absolute/path/to/golang.go-extension \
BROTE_TEST_DLV=/absolute/path/to/dlv make test-vscode
```

On Linux also set `BROTE_CODE_BIN` to the VS Code executable. The test needs a
working desktop/display, `go`, and `unzip`. It builds the VSIX, uses a temporary
profile and a copy of the Go extension, and launches a real Go process through
VS Code. It verifies the inspection tool, opens the native inline question
composer, steps and continues, then retrieves both exported traces from Tempo.
The captured local must equal `42`. Results are saved to
`dist/vscode-host-result.json`; failed runs retain their isolated profile/logs.
It does not install into your normal profile or require a model login. Generating
an actual model answer still requires a separately configured provider.

Core ownership, data locations, CLI/Pi query access and lifecycle details are documented in [embedded storage](embedded-tempo.md). Native DAP collection remains in the editor adapter; span construction, limits and export are in Go.
