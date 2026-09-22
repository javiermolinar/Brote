<p align="center">
  <img src="assets/brote-plant.png" width="180" alt="Brote, a friendly carnivorous plant that catches bugs">
</p>
<h1 align="center">Brote</h1>
<p align="center"><strong>Your LLM debugger companion.</strong></p>
<p align="center">Pause the program. Ask beside the code. Figure it out together.</p>

Start your normal VS Code debugger with **F5**, pause, and choose **Ask Brote About
Selection**. Brote captures the stack and variables, then keeps your conversation
beside the code in native comments. Use **Continue in Chat** for a longer discussion.
Your existing debug adapter and VS Code controls handle execution—no handover needed.

![Brote showing debugger state and a threaded conversation](assets/debugger-conversation.png)

*The earlier browser inspector illustrates the workflow; the VS Code extension uses native comments and Chat.*

## Install

```sh
curl -fsSL https://github.com/javiermolinar/Brote/releases/latest/download/install.sh | sh -s -- --editor vscode
```

Choose an available model when you ask your first question. Brote requires VS Code
1.138 or later. Go through Delve is the tested adapter; capture support for other
languages depends on their DAP implementation.

## OpenTelemetry traces

Set `OTEL_EXPORTER_OTLP_ENDPOINT` in VS Code's environment to export debugger
actions and captured program state to Tempo, Grafana Cloud, or another OTLP/HTTP
backend. Stable capture names and selected values help compare runs with trace diff.

[Tracing setup](docs/tracing.md) · [VS Code guide](packages/vscode/README.md)

```text
VS Code debugger ─── DAP ─── language adapter ─── program
        │
      Brote ─── native comments + Chat
        └── optional OTLP exporter ─── Tempo / Grafana Cloud
```

## Development

```sh
npm ci
make build
make test
make vsix
```

MIT licensed. Third-party notices are retained.
