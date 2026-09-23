<p align="center">
  <img src="assets/brote-plant.png" width="180" alt="Brote, a friendly carnivorous plant that catches bugs">
</p>
<h1 align="center">Brote</h1>
<p align="center"><strong>Go-native TDDD* with your LLM debugger companion.</strong></p>
<p align="center"><sub>*Trace-Driven Debugging and Development. Yes, another D.</sub></p>
<p align="center">Pause the program. Ask beside the code. Figure it out together.</p>

Brote brings Trace-Driven Debugging and Development to your Go debugger and AI
agent. Capture stacks, variables, and debugger actions as traces, then compare
runs to understand what changed and verify your fixes. Keep the conversation
beside the code, grounded in evidence you can revisit after the session ends.

Use **Codex or Pi** with the browser inspector, or stay in **VS Code** with native
debugger comments and Chat. All three use Brote's Go core to capture and store
traces locally. Go debugging is powered by **Delve**.

![Brote showing debugger state and a threaded conversation](assets/debugger-conversation.png)

*The browser inspector keeps debugger state and agent conversations beside the code;
the VS Code extension uses native comments and Chat.*

## Install

Choose your client below. The integrations use a precompiled Brote core for
**macOS or Linux, arm64 or x64**; no Go build is needed to install Brote.
Debugging Go programs requires [Delve](https://github.com/go-delve/delve).

### Codex

With the `codex` command available on your PATH:

```sh
curl -fsSL https://github.com/javiermolinar/Brote/releases/latest/download/install.sh | sh -s -- --agent codex
```

The installer verifies the release checksum and installs the Brote core, browser
inspector and Codex plugin.

### Pi

Requires Pi 0.85.1 or later and Node.js 22.19 or later.

```sh
pi install git:github.com/javiermolinar/Brote
```

Run `/reload` in Pi after installation. Pi manages the checkout; Brote downloads
and verifies the matching precompiled core automatically. To update:

```sh
pi update --extensions
```

Run `/reload` again after updating. See the [Pi guide](adapters/pi/README.md) for
offline installation and switching from the shell installer.

### VS Code

With the `code` command available on your PATH:

```sh
curl -fsSL https://github.com/javiermolinar/Brote/releases/latest/download/install.sh | sh -s -- --editor vscode
```

Requires VS Code 1.138 or later and Delve. Start a Brote **F5** configuration
or use **Brote: Attach to Session**, pause, and choose **Ask Brote About Selection**. Pick an
available model when prompted; answers appear in native comment threads.
Use **Continue in Chat** for a longer discussion.

## Try it with Codex or Pi

Open your Go project in Codex or Pi and ask:

> Let's debug this project with Brote. Start a session, stop at `main`, and
> give me the debugger URL. Leave it paused so I can explore.

Open the URL to inspect the source, stack and variables. Select code or a variable
and ask where its value comes from; the answer stays with the captured evidence.

[Debugging guide](docs/debugging.md) · [Pi guide](adapters/pi/README.md) · [VS Code guide](packages/vscode/README.md)

## Try it from a terminal

With `brote` and `dlv` on your PATH, run this from a checkout of Brote:

```sh
go build -gcflags='all=-N -l' -o /tmp/brote-demo ./examples/demo
brote session start --binary /tmp/brote-demo --project "$PWD/examples/demo" --thread ''
```

The program starts paused. Replace `SESSION` below with the returned session ID:

```sh
brote debug breakpoint add SESSION --function main.process
brote debug continue SESSION --human --wait 20s
brote debug eval SESSION --expression attempt
brote debug step SESSION --over --human --wait 20s
brote session stop SESSION --confirmed
```

This stops at `main.process`, reads `attempt`, steps one line, then ends the run.
You can also open the returned inspector URL. `--human` marks commands you run
yourself; `--thread ''` starts without connecting an agent conversation.

## OpenTelemetry traces

Brote embeds [Grafana Tempo](https://github.com/grafana/tempo) to store traces locally
with zero configuration, making **TDDD (Trace-Driven Debugging and Development)** a
first-class workflow: capture debugger state as traces and compare runs. The bundled
runtime is managed by Brote; its internal listeners bind only to loopback.

The Go core captures and stores traces for VS Code, Codex and Pi through one Brote API.
Use `brote query traces` and `brote query traces TRACE_ID` to inspect saved evidence.

Set `OTEL_EXPORTER_OTLP_ENDPOINT` in Brote's environment to export debugger
actions and captured program state to Tempo, Grafana Cloud, or another OTLP/HTTP
backend. Stable capture names and selected values help compare runs with trace diff.

[Tracing setup](docs/tracing.md) · [VS Code guide](packages/vscode/README.md)

```text
VS Code debugger ─── DAP ─── language adapter ─── program
        │
      Brote ─── native comments + Chat
        └── Brote Go core API ◄── Codex / Pi / CLI
                    ├── embedded Tempo
                    └── optional OTLP ─── Tempo / Grafana Cloud
```

MIT licensed. Third-party notices are retained.

## Build from source

Use the Go version in `go.mod`, Node.js 22.19 or later, and Delve for debugging:

```sh
npm ci
npm run build
go build -o bin/brote ./cmd/brote
npm test
go test ./...
```

Use `make vsix` to package the VS Code extension. Release bundles include Brote
and unmodified Tempo source, dependency checksums, licenses, and notices.
