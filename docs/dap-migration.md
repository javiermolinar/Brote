# DAP backend migration

New sessions default to DAP. The broker owns one long-lived backend connection;
its editor endpoint shares that connection rather than opening another Delve
client. Browser and harness HTTP commands are normalized into DAP operations.

Implemented: precompiled launch via headless Delve, conditional/function
breakpoints, threads/goroutines, stack/scopes/variables, bounded expression
expansion, execution, events, interruption and explicit termination. DAP editor
frame/variable references are remapped and invalidated across generations.
Breakpoint sets merge editor requests with agent/browser-created breakpoints.
Editor disconnect never forwards target termination to the backend.

Go-specific metadata exceptions remain explicit:

- Delve remote-attach DAP does not report the target PID/current running state:
  bootstrap/recovery reads identity via JSON-RPC before initializing DAP. If a
  recovered target was already running, recovery retains the legacy RPC backend:
  Delve DAP remote attach halts running targets. It must not silently change
  execution merely to migrate transport. A new session defaults to DAP.
- Delve does not implement `loadedSources`: binary source discovery still uses
  read-only JSON-RPC, including the source picker and captured source validation.
- Recovery reads existing breakpoints through JSON-RPC because DAP provides no
  request to enumerate an existing backend's breakpoint configuration.

Execution, breakpoint writes, stack/variables and evaluation use DAP. These
metadata exceptions are isolated from normal execution; they are not a second
execution owner. A future Go metadata provider can replace them without changing
frontend APIs. Do not claim the backend is completely RPC-free yet.

Old session descriptors without `backend` recover with their original RPC path.
`start --backend rpc` remains an explicit migration fallback, not an automatic
retry of failed DAP execution. The live pre-migration session is not upgraded by
building or installing the new version.
