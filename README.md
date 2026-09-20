# Delve LLM Adapter

Pass the same live Go debugger between an agent and a human. Start an existing debug
binary, set conditional breakpoints, inspect stack and locals, then hand the paused
process to the browser, VS Code, or Zed. Returning control never resumes execution.

The shared Go core provides a JSON CLI, a local HTTP API, durable SSE events, and an
embedded TypeScript browser inspector. Codex and Pi have small notification hooks;
all debugging operations use the same CLI. No MCP, central daemon, or embedded editor.

## Install

Requires macOS/Linux (arm64/amd64), compatible Delve, and your selected harness.
The browser inspector is always included. VS Code integration is optional.

Once GitHub Releases are published, replace `OWNER/REPO` with this repository:

```sh
curl -fsSL https://github.com/OWNER/REPO/releases/latest/download/install.sh \
  | sh -s -- --agent codex
# Or: --agent pi --editor vscode
# Pin a release: add --version v0.2.0
```

The release installer embeds its repository. When running the source `install.sh`,
pass `--repository OWNER/REPO`. No public remote/release is configured in this checkout.

Alternatively extract a release bundle and run:

```sh
./delve-llm-adapter/bin/delve-llm-adapter setup --agent pi --editor vscode
```

Setup installs under `~/.local/share/delve-llm-adapter`, with launchers in
`~/.local/bin`. It prints PATH instructions without editing shell startup files.
Set `DELVE_LLM_ADAPTER_HOME` for an isolated installation (its launchers use `bin/`
inside that directory). `installation`, `repair`, and `uninstall --component
codex|pi|vscode|core` manage only this installation. Rerunning setup preserves and
refreshes existing integrations. Live debugger processes are never killed by setup.
Core removal refuses active sessions and installed integrations.

## Debug

```sh
delve-llm-adapter start --binary /path/to/precompiled-app --project /path/to/source
delve-llm-adapter break SESSION --file main.go --line 42 --condition 'attempt == 3'
delve-llm-adapter continue SESSION --wait 20s
delve-llm-adapter handover SESSION --editor browser --note 'Inspect total'
```

Open the returned panel URL. Take control in the browser, step and inspect, then
return to the agent. VS Code uses the optional companion; Zed attaches through F4.
The target is never rebuilt during handover.

Codex automatically binds `CODEX_THREAD_ID` when present; use `--thread ""` for a
standalone session. In Pi, use the `debug_connect` tool or `/debug-connect SESSION`
after starting to bind the current conversation and enable handback notifications.

`events SESSION --cursor N` emits a persistent JSONL event stream. Without a wakeup
hook, `await-control SESSION --cursor N --timeout 20s` returns handback to an active
tool call. An idle harness requires a notification hook to start another turn.

## Boundaries

Local prototype: HTTP binds only to `127.0.0.1`, without bearer authentication.
Other local processes can inspect/control the debugger. Host/Origin checks and
JSON-only mutations remain. Owner labels and client bindings coordinate clients;
they are not security credentials. Do not expose the API to a network.

Each session has one broker, one Delve process, and one target. Broker recovery
reuses the target; editor/client disconnect does not kill it. Notification delivery
is durable but not exactly-once; ambiguous deliveries require explicit retry.

## Development

```sh
go test ./...
go vet ./...
DH_INTEGRATION=1 CODEX_THREAD_ID='' go test -race ./...
npm ci
npm run build
npm test
npm run package:vscode
python3 scripts/release.py --version v0.2.0
```

Source launcher: `scripts/debug-handover`. It compiles a cached helper, not the target.
Release bundles contain prebuilt executables and need no Go or Node build tooling.

See [architecture](docs/architecture.md), [protocol](docs/protocol.md), and
[debugging guide](docs/debugging.md). MIT licensed; third-party notices are retained.

### Switching sessions

Expand **Sessions** in the browser inspector and select **Open session**. The list shows project, binary, status, and execution owner. Opening another session does not step, stop, transfer control, or rebind an agent conversation. Offline sessions show recovery guidance; ended sessions cannot be opened. Use the selected inspector’s existing handover controls for VS Code or Zed, or Pi’s `debug_connect` to explicitly bind an existing session.

Use **End session** in the session list to explicitly terminate a live target and its debugger, regardless of which frontend owns execution. This asks for confirmation. Offline brokers must be recovered before ending; the manager does not kill saved PIDs. Closing Pi alone preserves the debugger: restart in the same project with `pi -c` or choose the conversation with `pi -r`. The Pi adapter reconnects matching session bindings on startup.
