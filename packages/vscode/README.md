<p align="center"><img src="assets/brote-plant.png" width="120" alt="Brote, your bug-catching debugger companion"></p>

# Brote for VS Code

Pause your program with **F5**, select a source line, and choose **Ask Brote About Selection**. Pick a model and discuss the captured stack and variables in native inline comments. Use **Continue in Chat** for a longer conversation or `#brote` to inspect the selected frame from Chat.

Brote uses your existing debug adapter. It needs no broker, separate debugger, or handover. Normal VS Code controls own breakpoints, stepping, and execution. Questions and answers persist in workspace storage; follow-ups capture the current pause or explicitly use historical evidence after debugging ends.

Set `OTEL_EXPORTER_OTLP_ENDPOINT` in the VS Code extension host environment to send debugger actions and program snapshots to an external OTLP/HTTP backend such as Tempo or Grafana Cloud. Breakpoint stops capture the reported thread; explicit questions capture the selected frame. Brote never automatically resumes the program.

`brote.capturePoints` maps exact DAP function names to stable snapshot names and selected local variable names:

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

Captures are bounded and depend on adapter support. Generic DAP cannot identify goroutine creation sites or reconstruct spawn relationships. Threads describe observed state, not their full lifetimes. Traces are best effort; stopping the session exports the parent spans. Authentication uses `OTEL_EXPORTER_OTLP_HEADERS` with percent-encoded values. Restart VS Code after changing its environment. Saved evidence and exported snapshots contain application data.

VS Code 1.138 or later and an available language model provider are required. Automated tests cover native captures, persisted discussions, DAP action ordering, and HTTP export. Authenticated model-provider behavior still needs a manual check.
