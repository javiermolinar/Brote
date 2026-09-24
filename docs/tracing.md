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

## Conversations and optional annotations

Questions and replies are exported to the Brote `debugger` trace. Explicit notes
are exported to the `program` trace. Run `brote query traces --session SESSION`
to discover exact service sessions (`SESSION:EXECUTION_RUN` for shared Go runs),
trace IDs, and the persisted `captures` map. Source lines alone are not identities.

```sh
brote annotation create SERVICE_SESSION --id note-1 --capture CAPTURE_ID --body 'Observed total is 42' --author agent
brote annotation list SERVICE_SESSION
brote query traces PROGRAM_TRACE_ID
```

Omit `--capture` to deliberately target the verified program root, including
legacy saved pauses without capture IDs. An unknown capture is rejected, never
silently converted to a root target. `--body-file PATH` accepts a UTF-8 note.
`--label` and `--comparison-key` are optional. Nothing adds annotations automatically.

For a two-run comparison, use `--targets-file targets.json` with an array of
`{"session":"SERVICE_SESSION","traceId":"PROGRAM_ID","spanId":"CAPTURE_SPAN_ID","captureId":"CAPTURE_ID"}`
objects, one from each run in the same investigation, including the owning session.
Use actual values from `query traces`; do not construct span IDs. The same explicit
annotation is published into both program traces. `--conversation-file PATH`
accepts a target object identifying an existing conversation span in its debugger
trace; discover it with `query traces DEBUGGER_TRACE_ID`.

Keep `--id`, `--revision` (initially 1), author, body, and targets unchanged on
retry. To revise a note, increment the revision by one. List returns latest
revisions; historical revisions remain on disk and in exported traces. A successful
write means **saved locally**; inspect `export` for independent local/remote
`pending`, `accepted`, `failed`, or `not_configured` states. Acceptance does not
prove query visibility. Saved-run operations start no debugger. See the
[metadata schema](trace-metadata.md) for limits, routing and recovery behavior.
