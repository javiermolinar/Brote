# Tracing examples

Brote stores traces locally without configuration. In VS Code, use
**Brote: Session Traces** to list them and open saved JSON.

## Capture values at a line

With a paused Brote session, replace the source path and line with your capture point:

```sh
brote debug tracepoint add SESSION --file /absolute/path/main.go --line 20 --values '{"total":"total"}'
brote debug continue SESSION --human --wait 20s
brote debug captures SESSION
```

`--human` is for your own terminal commands. Agents use
[bounded task execution](debugging.md#let-an-agent-execute-a-bounded-task).

## Read traces after debugging

```sh
brote query traces
brote query traces --session SESSION
brote query traces TRACE_ID
```

Copy a trace ID from the listing. Saved traces remain available after the target
exits. New captures can take tens of seconds to become queryable; retry if the
result is pending. Interrupted writes or a full disk can lose recent captures.

## Export to an OTLP backend

Set the environment before starting Brote or the editor that launches it:

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
# If authentication is needed, percent-encode the header value:
# export OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer%20TOKEN'
code .
```

For a full traces URL, use `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` instead. Export
uses HTTP/protobuf. Restart an existing Brote tracing service/editor to pick up
environment changes. Local storage remains enabled if remote export fails.

## Label VS Code captures for comparison

Set a breakpoint in `main.process`, then add this to `.vscode/settings.json`:

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

Keys match exact frame names. Values select already captured locals; this setting
does not create breakpoints or evaluate expressions. Keep names stable across runs.
