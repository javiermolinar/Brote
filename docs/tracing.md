# Service-owned traces

Configure the environment before starting a shared CLI session:

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
# Optional percent-encoded header values:
# export OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer%20TOKEN'
brote start --binary /absolute/path/program
```

Tempo or another receiver runs separately. Brote embeds neither Tempo nor a
Collector. CLI-only sessions export automatically; attaching or disconnecting
VS Code does not transfer exporter ownership. Configuration is saved in private
launch settings for recovery and restart.

The generic endpoint receives `/v1/traces`.
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` overrides it with a full URL; traces-specific
headers and protocol override generic values. Only `http/protobuf` is supported.
Invalid configuration disables export with a credential-free diagnostic in state.
Use headers for authentication, not URL credentials.

## Captures and trace structure

```sh
brote tracepoint add SESSION --file /absolute/path/main.go --line 12 \
  --name process.result --values '{"total":"total","count":"len(items)"}' \
  --capture-limit 100
brote tracepoint list SESSION
brote captures SESSION
brote state SESSION --brief
```

Tracepoints use stable IDs and revisions shared by the CLI and VS Code. Selected
values are read-only expressions, with at most 16 selections. Exact selected scalar
strings become `program.value.<alias>`. Conditions and hit conditions control when
Delve stops; the capture limit separately bounds attempts per definition per run.
Source tracepoints are supported; function breakpoints are ordinary breakpoints.

Each run produces a program trace and a debugger trace. The program run links to
the `debugger.session` root. Snapshot spans sit beneath observed-goroutine spans,
with stable capture names and bounded state. Debugger action spans record accepted
or acknowledged actions; they do not measure target execution duration. State
returns both trace IDs.

Snapshots are instantaneous observations. Goroutine spans describe observation
windows, not goroutine lifetimes or spawn relationships. Debugger pauses distort
wall-clock timing. Parents finish on service shutdown or restart, so children may
arrive before their parents. Editor disconnect does not end these spans.

Exclusive verified tracepoint hits automatically resume only after a complete
capture and unchanged execution intent, configuration, run, and pause epoch.
Ordinary/mixed breakpoints, manual Pause, steps, uncertain attribution, capture
errors, truncation, and exhausted limits preserve the pause. Human interruption
cancels agent execution. Disabled or failed export is independent of capture success.

## Bounds and delivery status

Captures share a two-second deadline, bounded value expansion, and a 128 KiB record
limit. The service retains the newest 64 capture records. Per-run attempt counts
survive recovery. Exported snapshot JSON is limited to 32 KiB; larger records can
be captured successfully while export fails. Configure the receiver's attribute
limits accordingly, for example Tempo `distributor.max_attribute_bytes: 65536`.

Export uses a 256-span queue, batches of 64, a two-second transport timeout, and a
three-second shutdown deadline. Overflow, receiver failure, crashes, and shutdown
can lose spans. Capture outcomes (`captured`, `skipped`, `truncated`, `failed`) are
separate from export status (`disabled`, `queued`, `sent`, `skipped`, `failed`).
`sent` means the receiver accepted the export; it does not prove Tempo queryability.

## Verification

```sh
go test ./internal/telemetry ./internal/broker
python3 scripts/check-shared-service.py
BROTE_CHECK_OTLP=1 OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 \
  python3 scripts/check-shared-service.py
make vsix
BROTE_HOST_SCENARIO=otlp node packages/vscode/test/host/shared-run.cjs
BROTE_HOST_SCENARIO=f5 node packages/vscode/test/host/shared-run.cjs
```

The real service test retrieves both traces from Tempo at localhost:3200 and
checks exact selected values and parentage. The packaged VS Code test uses an
isolated profile and real Go process. It combines CLI/UI tracepoints and an
ordinary breakpoint, verifies five snapshots with values `7, 8, 8, 9, 9`, and
checks no duplicate extension export. Results are written to
`dist/shared-otlp-host-result.json`. It needs a working desktop, Go, Delve, VS Code,
and local Tempo; it does not need a model login.

Verified locally on macOS arm64 with Go 1.27.1, Delve 1.27.1, and VS Code 1.138.0.
Remote extension hosts and Linux desktop workflows have not been verified here.

The F5 scenario launches source, a selected Go test, and an executable using the
packaged CLI. It verifies initial tracepoint/ordinary breakpoint ordering, CLI
inspection of the same session, restart/new run, editor-only disconnect, explicit
termination, and failed-build cleanup. Evidence is saved in
`dist/shared-f5-host-result.json`.
