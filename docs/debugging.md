# Debugging guide

Run commands from the repository or extracted plugin root. For development and the project layout, see the [README](../README.md).

A local prototype for sharing a **live Go debug session between Codex and Zed or VS Code**. Start with a precompiled binary, stop on a condition, inspect it with the model, take over in your editor, then give the same paused process back to Codex.

The browser inspector shows ownership, execution state, call stack, selectable goroutines and frames, source, locals, arguments, and shared breakpoints. It polls the live debugger every 800 ms. No telemetry or remote service is involved.

## Try it

Requirements: Go 1.23+, a compatible Delve installation, and Zed with `zed` on PATH, or VS Code with `code` on PATH and the companion extension. Automatic inspector handovers also require a Codex CLI exposing `queue --thread`; `doctor` checks this capability. The prototype targets macOS/Linux; its full UI handover was tested on macOS arm64, Go 1.27.1, Delve 1.27.1, Zed 1.20.2, and VS Code 1.137.0.

From this directory, build the included sample **once**:

```sh
go build -gcflags='all=-N -l' -o examples/demo/demo ./examples/demo
./scripts/debug-handover start --binary examples/demo/demo --project examples/demo
```

Copy the returned session ID in place of `ID` below. Open the returned `panel` URL in Codex's browser panel or your browser.

```sh
./scripts/debug-handover break ID --file main.go --line 18 --condition 'attempt == 3'
./scripts/debug-handover continue ID --wait 20s
./scripts/debug-handover handover ID
```

When using the Codex skill with native desktop automation, Codex selects **Debug Handover · ID** in Zed and verifies the paused session before handing it to you. The browser button queues an attachment request to the bound Codex task. For a standalone session without a bound task, press **F4** in Zed and select that profile. The process is paused immediately before `total += delta`, with `attempt=3`, `total=21`, and `delta=21`. Step over once: `total` becomes `42`. The browser inspector follows the change.

Click **Give control to Codex** in the inspector, or run:

```sh
./scripts/debug-handover reclaim ID
./scripts/debug-handover state ID
./scripts/debug-handover next ID --wait 20s
```

Reclaim closes the editor's frontend session. The process keeps its PID, memory, goroutines, and breakpoints. Another `handover ID` lets the selected editor reattach at the new pause. Closing a window does not end the session. To terminate the process and debugger explicitly:

```sh
./scripts/debug-handover stop ID
```

## VS Code companion

Install the companion VSIX once, then select the editor:

```sh
code --install-extension /path/to/debug-handover-0.1.0.vsix
./scripts/debug-handover handover ID --editor vscode
```

The CLI opens the project. In a trusted local workspace, the extension calls VS Code's public `debug.startDebugging` API and connects a `DebugAdapterServer` to the existing broker. No profile picker, launch.json, Go extension, or target compilation is required to attach. Normal workspace trust prompts still apply. The inspector has an **Editor** selector; the last editor is remembered per session.

In VS Code, click **Give control to Codex** in the status bar or run **Debug Handover: Give Control to Codex** from the command palette. Pause first: handback does not silently interrupt execution. The broker detaches the frontend and notifies the linked Codex task. **Debug Handover: Open Live Inspector** opens the shared browser panel.

The extension checks private session descriptors every second, only for exact canonical project directories open in trusted local workspace folders. A fresh request, VS Code ownership, a settled pause, and no existing editor connection are required. Attempts are remembered across reloads so deliberately disconnecting does not reopen the debugger. **Debug Handover: Attach Pending Session** explicitly retries; another CLI handover also creates a fresh request. A recovered broker creates a fresh request for a VS Code-owned session; automatic attachment waits for a settled pause.

For a custom `DEBUG_HANDOVER_HOME`, set the application-level **Debug Handover: Session Directory** preference if VS Code did not inherit the environment variable. Attach errors appear in the **Debug Handover** output channel and the inspector. Remote and untrusted workspaces are unsupported. The VSIX is a local prototype, not published to the Marketplace.

Build and package from the plugin root:

```sh
npm ci
npm run build
npm run package:vscode
```

Source is in `editors/vscode/src`; its bundled output requires no Node installation for normal use. Protocol tests cover fresh request admission, stale request suppression, local endpoint validation, API errors, and redirect rejection. Real Delve integration tests exercise both Zed and VS Code ownership, stepping, handback, and safe disconnect.

## Use your existing binary

```sh
./scripts/debug-handover start --binary /work/bin/service --project /work/service -- --config dev.yaml
./scripts/debug-handover start --binary /work/bin/service.test --project /work/service -- -test.run TestCheckout
```

`start` always uses `dlv exec`: it does **not** rebuild the target. The project is also the process working directory. Use `--dlv /path/to/dlv` if needed. Delve is discovered on PATH or in `~/go/bin/dlv`.

The executable needs compatible debug information; don't strip it with `-ldflags='-s -w'`. `-gcflags='all=-N -l'` disables optimization and inlining for more predictable inspection. It is a build-time choice, not something repeated during handover. Source paths must match the executable's build paths in this version. The broker records the binary SHA-256, available VCS build revision, and project source hashes at launch. It warns when the inspected source changes or binary metadata changes. Missing build provenance is explicitly unverified; launch-time hashes do not prove which sources produced an existing binary.

## Codex plugin

The repository also serves as a Codex plugin containing a CLI-backed `debug-handover` skill. The repository name is `delve-llm-adapter`; the plugin, CLI, VS Code extension, and session storage retain their `debug-handover` identifiers. The personal-marketplace installation uses:

```sh
codex plugin add debug-handover@personal
```

That command assumes this plugin has been registered in **your** personal marketplace. A copied directory does not create the recipient's marketplace entry automatically. The core CLI works independently of Codex installation. The launcher builds its own helper once per source change and caches it under `~/.cache/debug-handover` (or `XDG_CACHE_HOME`). The helper has no third-party Go dependencies. Its browser UI bundles Highlight.js locally; normal debugging requires no Node.js installation or CDN access.

When developing from a checkout elsewhere, a local `~/plugins/debug-handover` symlink can point to the repository so an existing personal-marketplace entry continues to resolve. Release archives keep the `debug-handover/` plugin directory name regardless of the checkout's name.

Example prompts after installation:

- “Use this Go binary, break in worker.go at line 120 when attempt == 3, then hand it to me in Zed.”
- “I stepped through it in Zed. Take it back and explain why the value changed.”
- “Show the caller's locals without resuming.”

The skill becomes discoverable in a new Codex task after installation. The current task can use the CLI directly. This is a community-style prototype, not an official OpenAI, Microsoft, Zed, or Delve integration.

## Automatic handover messages

`start` uses `CODEX_THREAD_ID` when available, or an explicit `--thread UUID`. It resolves the Codex executable and checks for `queue --thread` support. Pass `--thread ""` to start without task notifications. A live session can be linked or unlinked with:

```sh
./scripts/debug-handover bind ID --thread UUID
./scripts/debug-handover bind ID --thread ""
```

VS Code attaches directly through its companion and does not queue an attachment message. The inspector's **Hand over to Zed** button opens the project and queues a message to the linked Codex task. The agent completes Zed's picker using its desktop tools and verifies the pause. **Give control to Codex** detaches the editor frontend, then queues a handback message. Codex reads the fresh stack and locals before continuing the discussion. It does not automatically resume the target. These messages remain visible in the task; existing model and permission settings are preserved.

CLI handovers stay quiet by default, preventing loops when Codex is already handling a notification. Add `--notify` to request a message explicitly. Delivery status persists with the session and appears in the inspector. A failed delivery can be retried with `retry-notification ID`; an interrupted delivery is marked unknown and is never retried automatically because the original message may already have arrived. Check the task before retrying. Accepted messages may wait behind an active Codex turn. This uses a capability of the installed CLI, not an always-available API guarantee; older Codex versions can use the manual flow.

## Inspect expressions and watches

```sh
./scripts/debug-handover eval ID --expression 'len(items)' --goroutine 12 --frame 0
./scripts/debug-handover eval ID --expression 'items[128:256]' --depth 6 --count 128
./scripts/debug-handover watch ID --expression 'total'
./scripts/debug-handover unwatch ID --expression 'total'
```

The browser inspector has the same read-only evaluator, an Inspect button beside each local, expandable nested values, and persistent watches. Values follow the selected goroutine and frame and are discarded on execution or disconnect. Watches show per-expression errors when a variable is out of scope. Evaluation rejects calls, assignments, and channel receives; supported builtins are len, cap, real, imag, and complex. Collection limits apply at each level, so large values may be partial; use slicing for another range.

## Recover and clean up

```sh
./scripts/debug-handover sessions
./scripts/debug-handover recover ID
./scripts/debug-handover cleanup ID
./scripts/debug-handover doctor --binary /work/bin/service --project /work/service
```

A new broker connects to the recorded Delve endpoint and verifies the target PID. It preserves ownership, watches, breakpoints, the authentication token, and the stopped process. A lock prevents two brokers from managing the same session. It reuses HTTP/DAP ports where possible and updates only its generated Zed profile if a port changed. Reopen the returned panel URL if the HTTP port changed. Browser refreshes reconnect automatically when the original endpoint returns.

Delve runs independently with file-backed logs. A broker crash may leave a running target running; recovery reports its real state. Explicit stop terminates the session and removes its generated profile. `cleanup` removes a leftover profile only after known processes have ended, preserving unrelated JSONC profiles and comments and retaining diagnostic logs. Recovery cannot resurrect a dead target, and sessions created by the older prototype lack the necessary recovery metadata.

## How it works

```text
Codex skill / CLI ── HTTP ──┐
                           │
Live browser inspector ────┤── Session broker ── JSON-RPC ──┐
                           │                              │
Zed / VS Code ── DAP ── ownership proxy ─────────────────────────── Delve ── existing binary
```

The broker launches one `dlv exec --headless --accept-multiclient` process. Codex inspection and commands use Delve JSON-RPC; the editor connects through a DAP proxy to that same Delve server. The proxy forces remote attach with `stopOnEntry=true`, avoiding an accidental resume when the editor completes initialization.

For an existing pause, the proxy also replaces Delve's hardcoded attach thread ID with the actual stopped goroutine. This keeps the editor focused on a breakpoint inside a worker or test goroutine instead of jumping to the waiting main goroutine.

One side owns execution at a time. The broker rejects stale action generations and Codex execution while an editor owns the session. Reclaim requires a settled pause, emits a DAP `terminated` event, and closes the frontend connection without killing the target. Editor disconnect requests are forced to preserve the debuggee. Launch, restart, and terminate requests through the proxy are rejected; the explicit broker stop action owns termination.

Codex breakpoints have a separate name prefix. Delve's DAP adapter reconciles its own source/function breakpoint sets without deleting those RPC-created breakpoints. The inspector lists both. Delve still rejects two independent breakpoints at the exact same address. Removing or editing a Codex breakpoint is done through Codex/the inspector; Editors do not necessarily show every external breakpoint in its own breakpoint list.

Session descriptors and logs are stored under the OS user cache directory in `debug-handover/sessions/ID`. Override with `DEBUG_HANDOVER_HOME` for isolated tests. The descriptor is mode 0600 and the session directory 0700. All listeners bind to loopback. HTTP actions require a random session token and reject foreign origins/hosts. The DAP/Delve ports assume a trusted local machine; this is not a security boundary against other local processes or users.

## Prototype boundaries

- **Zed attachment uses desktop automation.** A bound Codex task can wake from the inspector and complete the profile picker. Zed still has no native debugger-start CLI/URL entry point. If task notifications or desktop tools are unavailable, use F4. The panel reports the frontend connection; the agent also checks the actual debugger UI.
- **Handover preserves the process, not the editor tab.** Reclaim ends the editor's frontend; the next handover reattaches.
- **Local launch only.** Existing Go executables and test binaries work. Attaching to an arbitrary PID, remote/container source mapping, core files, reverse execution, and hot reload are outside this version.
- **Bounded inspection.** At most 30 stack frames, 100 goroutines, 16 watches, six evaluation levels, and 128 collection entries per level. Function execution, assignments, and unrestricted REPL evaluation are outside this version.
- **Recovery depends on Delve surviving.** Debugger/target crashes and machine restarts lose process memory. Source fingerprints flag changes but cannot establish missing build provenance.
- **No automatic source rebuild.** Rebuild and start a new session when code changes. Existing memory cannot survive replacing the executable.

## Verification

The inspector is written in strict TypeScript at `ui/inspector/src/app.ts`. Highlight.js supplies Go syntax colors, with a separate gutter and execution-line overlay. Build the committed browser bundle with Node.js and npm:

```sh
npm ci
npm run build
npm test
```

`ui/inspector/public/app.js` is the generated bundle embedded by Go. Rebuild it after changing TypeScript. Node.js is only needed for frontend development. The Highlight.js license is included in [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).

To preview new UI assets against an **existing** live debug session, pass the path to its session descriptor:

```sh
npm run preview -- /absolute/path/to/sessions/ID/session.json
```

Open the printed panel URL. This local server proxies authenticated API requests to the existing broker, preserving its ownership and generation checks; it does not launch, rebuild, resume, or restart the debuggee. Rebuild TypeScript and reload the browser to see further changes. Stop this preview server with Ctrl-C when done; the original debug session remains alive.

```sh
go test ./...
go vet ./...
DH_INTEGRATION=1 go test -race -v ./...
```

The integration test exercises both main and worker goroutines, read-only evaluation under both owners, bounded collection inspection, persistent watches, a forced broker crash, recovery with stable process identity, occupied-port rebinding, stale-action rejection after recovery, and profile cleanup. It also verifies that recovered Delve processes end when stopped. Each case builds one target, starts it through the public CLI, hits a conditional breakpoint, attaches a DAP client, steps, reclaims via RPC, reconnects, and checks the selected goroutine, PID, locals, both breakpoint sets, and unchanged target binary hash. It also checks that a frontend asking to terminate on disconnect cannot kill the session. Unit tests cover JSONC preservation and targeted removal, framing, stale actions, expression restrictions, durable notification delivery/failure using a local stub, source fingerprints, and the HTTP token/origin boundary. `.github/workflows/test.yml` defines clean macOS/Linux runs with pinned Delve; those hosted jobs have not been run from this unpublished local directory. Native Zed, native VS Code automatic attachment/handback, and real Codex delivery were verified separately with a paused Tempo test binary.

Package a shareable archive without targets, session tokens, logs, or node_modules. The VS Code extension is optional; add `--include-vsix` to bundle it after running `npm run package:vscode`:

```sh
python3 scripts/package.py --output dist/debug-handover-prototype.zip
```

Protocol references: [Delve APIs](https://github.com/go-delve/delve/blob/master/Documentation/api/README.md), [Delve DAP lifecycle](https://github.com/go-delve/delve/blob/master/Documentation/api/dap/README.md), [Zed Go debugging](https://zed.dev/docs/languages/go), [Zed CLI](https://zed.dev/docs/reference/cli), and [VS Code debugger APIs](https://code.visualstudio.com/api/references/vscode-api#debug).
