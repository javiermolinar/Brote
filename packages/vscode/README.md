<p align="center"><img src="assets/brote-plant.png" width="120" alt="Brote, your bug-catching debugger companion"></p>

# Brote for VS Code

Brote connects VS Code to CLI-managed Go debugging sessions. Its packaged CLI
transports DAP and semantic commands to the session service. The service owns
Delve, breakpoint reconciliation, capture and continuation. The shared Go tracing
service owns embedded Tempo and optional remote OTLP export.

## Attach to a session

Start a target with `brote start --binary /absolute/path/program`, then
run **Brote: Attach to Session**. The picker discovers existing service sessions.
An explicit configuration is also available:

```json
{
  "name": "Brote: attach",
  "type": "brote",
  "request": "attach",
  "sessionId": "SESSION_ID"
}
```

The workspace must be trusted. VS Code 1.138 or later, Go, and Delve are required.
`brote.runtimePath` can override the packaged CLI with a development build.
Existing `type: go` configurations remain independent; Brote capture and tracepoint
commands require a `type: brote` session.

## Launch with F5

Choose **Brote: Go program**, or add a configuration:

```json
{
  "name": "Brote: Go program",
  "type": "brote",
  "request": "launch",
  "mode": "debug",
  "program": "${workspaceFolder}",
  "tracepoints": [{
    "file": "${workspaceFolder}/main.go",
    "line": 12,
    "name": "work.result",
    "values": {"total": "total"}
  }]
}
```

Use `mode: test` and `args: ["-test.run=^TestChosen$"]` for a selected test, or
`mode: exec` with a prebuilt executable. Source/test modes build with debug symbols.
Supported settings include `args`, `cwd`, `env`, `envFile`, `buildFlags`,
`dlvToolPath`, `substitutePath`, and `stopOnEntry`. VS Code handles `preLaunchTask`
and `postDebugTask`. Interactive/external consoles and arbitrary Go extension
options are unsupported and rejected.

The CLI creates a paused session; initial tracepoints and editor breakpoints are
installed before configuration completes. Execution then continues unless
`stopOnEntry` is true. A startup that never finishes configuration expires after
30 seconds. Build/start failures report errors; cancellation cleans up newly
created sessions. F5 sessions appear in `brote sessions` and accept the same CLI
commands as terminal-created sessions. Configure OTLP in `env`, `envFile`, or the
launching VS Code environment for these sessions.

VS Code disconnect leaves the service and target available to the CLI or a later
editor. Use `brote end-session SESSION --confirmed` to end the target explicitly, or
`brote detach SESSION --human` to release an externally attached target. Detach
is rejected for Brote-launched targets; disconnect the editor to keep the session
alive, or explicitly terminate it. Only one editor can attach at a time.
DAP restart preserves session identity and the editor connection, creates a new
run/PID from the saved executable, and reinstalls initial breakpoints before
continuing. CLI restart disconnects the old editor; attach again afterward.
Restart does not rebuild changed source. Use a new F5 launch to rebuild.

## Tracepoints and questions

Right-click a Go source line and choose **Brote: Add Tracepoint**. A diamond marks
the location; the **Brote Tracepoints** panel shows definitions and capture/export
feedback. Edit, disable, or remove points there. CLI changes appear in the same
panel. `#broteTracepoints` exposes the semantic operations to VS Code agents.

Selected values are read-only Go expressions as JSON, for example
`{"total":"total", "count":"len(items)"}`. Capture names are stable labels for trace
comparison. Limits default to 100 attempts per run; reaching a limit preserves the
next stop. Configuration changes require a paused target and revision checks
prevent stale edits from replacing newer definitions.

Only exclusive verified tracepoint stops automatically continue after a complete,
bounded capture. Ordinary breakpoints, overlap, manual Pause, steps, uncertain
attribution, errors, and truncated captures stay paused. Disabled or failed OTLP
export does not prevent a successful capture from continuing.

Select a source line and choose **Ask Brote About Selection**. Explicitly choose
the attached agent or an available VS Code model. Use **Continue in Chat** to open
the same saved discussion; ordinary `@brote` Chat questions also persist through
the CLI. Questions, final answers and per-question evidence live in Go history
and appear in the CLI/browser. Workspace storage retains import receipts/backups
and UI preferences, not an authoritative conversation copy.

Follow-ups explicitly choose original evidence or the current paused frame.
After debugging ends, original evidence remains available without restarting it.
Cancellation and normal window reload persist an interrupted outcome; partial
streams are never final answers. Retry is explicit and uses a new attempt.
An OS-killed extension host cannot run its shutdown hook; inspect the saved state
and explicitly retry an interrupted provider request. Legacy native discussions
are backed up before idempotent import. A concurrent import may report busy;
reload after the other window finishes. Producing a provider answer requires a
separately configured model; selecting one never grants execution authority.

OTLP configuration belongs to the environment that starts the session. Closing
VS Code does not stop export. See the repository's tracing guide for endpoints,
status, bounds, and verification. Captures and exports contain application data.

## Embedded trace storage

The bundled Go tracing service stores captures in embedded Tempo automatically.
**Brote: Session Traces** lists shared and native-adapter traces and opens saved JSON.
Shared Brote sessions capture only in their broker; the extension does not create a
second producer. Other native debug adapters retain read-only DAP trace observation
through the same Go service. Their tracing does not grant Brote execution authority.
Discussion workflows above use Brote sessions and Go-backed history.

Local tracing requires no OTLP environment. Optional remote export is configured
on the shared tracing service at startup; local storage continues on remote failure.
See [embedded Tempo](../../docs/embedded-tempo.md) for query delay and durability limits.
