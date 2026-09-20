# AgentDebugger monorepo

One Go module and coordinated release version; frontends are separate npm workspaces.

- `cmd/agentdebugger`: primary CLI. `cmd/debug-handover` remains a compatibility entrypoint.
- `internal/broker`, `internal/dap`, `internal/session`: core, transport and durable state.
- `internal/cli`: commands and installation.
- `packages/client`: public TypeScript protocol client, independent of UI and Delve.
- `packages/web`: standalone inspector, embedded in the CLI bundle.
- `packages/vscode`: native VS Code integration, separately packaged.
- `adapters/pi`, `internal/agents/codex`, `skills`: harness integration.

Legacy CLI aliases, extension identifiers and storage locations remain compatible.
The `ui/inspector` and `editors/vscode` links preserve existing developer commands
and running preview processes; new code uses `packages/` paths.

Use `npm install`, `npm run build`, `npm test`, and `go test ./...` from the root.
Build the primary command with `go build ./cmd/agentdebugger`.
No authentication or MCP is added by the reorganization.

The next coordinated release is 0.3.0. Existing extension/plugin IDs and release
bundle directory names remain stable during migration; display names and the
primary CLI are AgentDebugger. This avoids duplicate editor installations.
