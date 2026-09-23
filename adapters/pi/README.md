<p align="center"><img src="../../assets/brote-plant.png" width="120" alt="Brote, your bug-catching debugger companion"></p>

# Brote for Pi

**Your LLM debugger companion.**

Requires Pi 0.85.1 or later and Node 22+. The release package bundles a native
core, browser inspector, extension and debugger skill. No Go build is needed.
A Go target still needs compatible Delve installed separately.

Install directly from GitHub:

```sh
pi install git:github.com/javiermolinar/Brote
pi update --extensions
```

Pi manages the checkout and updates. Installation downloads the exact-version
native core for your platform, verifies its checksum, and keeps it in a versioned
cache outside the checkout. If npm install scripts are disabled, the extension
prepares the same verified core on first load. No Go compiler is needed.
npm distribution follows later.

To switch from the shell installation, run `brote uninstall --component pi`
before installing the Git source. Keep only one Brote adapter enabled.

For an offline installation, extract the release's Pi tarball and use
`pi install /absolute/path/package`. Local-path packages do not fetch updates.

After installing or updating the adapter, run `/reload` in Pi.

- `/debug-sessions` lists current sessions, their projects, statuses, and browser URLs. Ended sessions are omitted; offline sessions remain visible.
- `/debug-connect SESSION_ID` connects this conversation and displays the inspector URL.
- `/debug-stop SESSION_ID` terminates that debugger session and its target process. Saved history is retained. An unavailable broker must be recovered before stopping the session.

The agent can use `debug_sessions`, `debug_connect`, and `debug_stop` for equivalent natural-language requests. Stopping requires an explicit user request for the named session.

Ask the agent to debug a program or test; it records your request with `debug_task`
(`start` plus the investigation instruction) and uses `debug_execute` to step or
continue under the Go-owned execution lease. No separate inspector approval is needed.
Inspector requests use `claim`; `complete` and `cancel` end the investigation's
execution scope. Attachment and debugger questions remain read-only.
Pi reports challenged active-turn, settled and shutdown facts; Go renews or
cancels the associated scope. An idle listener cannot renew execution;
forking does not inherit another conversation's grant. Uncertain delivery is
marked for review instead of being blindly replayed on reconnect.

Set `BROTE_BIN` to an absolute executable path only when overriding the
bundled core. Incompatible overrides fail visibly; the adapter does not silently
fall back to another binary. Supported native targets: macOS/Linux arm64/x64.

`debug_tracepoints` lists, creates, updates and deletes Go-owned definitions;
updates/deletes require their current owner/ID/revision. `debug_captures` returns
bounded capture outcomes, export failures and program/debugger trace IDs.
Configuration and capture reads do not resume the target. New CLI starts use the
shared service; old services without managed coordination must be updated and
recovered. The adapter relays canonical Go delivery messages and send receipts;
it owns no durable delivery cursor, retry reducer or lease timer.
