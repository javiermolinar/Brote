# delve-llm-adapter

Share a live Go debugging session between an LLM agent and a human. Start from a precompiled binary, stop on a condition, inspect it with the agent, then take over in the shared browser inspector or your editor. Handing control back preserves the same process and memory.

The working prototype ships as **Debug Handover**: a standalone Go CLI and broker, a TypeScript browser inspector, a Codex skill, and optional editor integrations. Codex, Zed, and VS Code have been tested. Pi support is planned.

## Layout

```text
cmd/debug-handover/       Executable entry point
internal/
  cli/                   Commands, HTTP client, launch/recovery, diagnostics
  broker/                Ownership, actions, snapshots, HTTP and DAP proxy
  session/               Session descriptors, persistence, binary provenance
  delve/                 Delve JSON-RPC transport and exit-state decoding
  dap/                   Debug Adapter Protocol message framing
  agents/codex/          Codex discovery, handover messages, queue invocation
  editors/               Editor identity and Zed profile management
ui/inspector/            Shared browser UI, embedded assets, preview server
editors/vscode/          Optional VS Code extension and its build output
skills/debug-handover/   Codex skill instructions
examples/demo/          Small Go target for trying the workflow
scripts/                Cached CLI launcher and release packaging
docs/                   Architecture and full debugging guide
```

The broker owns mutable execution state. Session storage and protocol transports do not depend on the broker or CLI. Agent notification delivery is separate from the broker's durable event bookkeeping. See [architecture](docs/architecture.md) for the dependency boundaries and the remaining work to support other harnesses.

## Try it

Requirements: Go 1.23+ and a compatible Delve installation. Normal CLI and browser use does not require Node.js or an editor. Automatic handback to Codex requires a CLI with `queue --thread` support; `doctor` checks it.

```sh
./scripts/debug-handover doctor
go build -gcflags='all=-N -l' -o examples/demo/demo ./examples/demo
./scripts/debug-handover start --binary examples/demo/demo --project examples/demo
```

Open the returned `panel` URL. Replace `ID` with the returned session ID:

```sh
./scripts/debug-handover break ID --file main.go --line 18 --condition 'attempt == 3'
./scripts/debug-handover continue ID --wait 20s
./scripts/debug-handover state ID
```

The sample is built once. `start` always uses an existing binary; handover never recompiles it. See the [debugging guide](docs/debugging.md) for editor attachment, precompiled test binaries, watches, recovery, Codex installation, and limitations.

## Development

```sh
go build -o bin/debug-handover ./cmd/debug-handover
go test ./...
go vet ./...
DH_INTEGRATION=1 go test -race -v ./...

npm ci
npm run build
npm test
```

The Delve integration tests use temporary programs and session directories. Generated inspector assets and extension JavaScript are committed; rebuild them after TypeScript changes. `npm run build:inspector` and `npm run build:vscode` build either frontend independently. `npm run preview -- /absolute/path/to/session.json` previews the inspector against an existing broker.

## Packaging

```sh
# Core/plugin source archive with the built inspector; no VSIX required.
python3 scripts/package.py --output dist/debug-handover-prototype.zip

# Optional editor extension, installed once in VS Code.
npm run package:vscode
code --install-extension editors/vscode/debug-handover-0.1.0.vsix
```

Add `--include-vsix` to the archive command to bundle the separately built extension. Archives exclude Git history, target binaries, session data, and node_modules.

The repository name is `delve-llm-adapter`; the plugin, CLI, extension, and session identifiers remain `debug-handover`. The repository also serves as the plugin source through `.codex-plugin/` and `skills/`. An existing personal-marketplace installation can continue to resolve through a `~/plugins/debug-handover` symlink to this checkout.

The prototype currently installs the agent plugin and optional VS Code extension separately. The intended product has one setup flow for the core, selected agent adapter, and optional editor companion; that unified installer is not implemented yet. Browser-inspector users need no editor extension.

This is a local prototype, not an official vendor integration or a published extension. Third-party licensing is recorded in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
