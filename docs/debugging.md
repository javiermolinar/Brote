# Shared service quick start

Use `brote start --binary /absolute/path/program` for a service-owned Go
session. `brote sessions`, `brote state SESSION --brief`, and `brote capabilities SESSION`
show identity and supported operations without attaching another debugger.
Use `brote pause SESSION --human` to interrupt running execution, then inspect
with `brote state SESSION`, `brote goroutines SESSION`, or `brote stack SESSION`.

`brote tracepoint add|update|remove|list` and `brote breakpoint add|update|remove|list`
manage durable definitions. Updates/removals require the current `--id` and
`--revision`; use `--scope run` for a definition that expires on restart.
`brote captures SESSION` reports bounded capture and separate export outcomes.
Attach VS Code with **Brote: Attach to Session**. Service DAP transport is
`brote dap SESSION`; direct Zed TCP handover is unsupported for this mode.

New starts use the shared service. Use `--legacy` (or `--service=false`) only for
old direct Zed/TCP handover; RPC additionally requires `--backend rpc`. These
sessions do not support shared definitions/captures or managed Pi coordination.
Existing sessions are not relabelled; `run-again` retains their stored launch mode.
See [the service contract](shared-service.md) and [agent contract](agent-contract.md).

Discussion history lives in Go storage. CLI, browser, inline comments and Chat
share question IDs, immutable evidence and recipient attempts. Choose an attached
agent or a VS Code model explicitly; retry preserves that destination. Original
context can be used after exit with `brote comment ... --offline`; current context
requires a live paused session. Neither history viewing nor answering starts a
debugger. Native VS Code history is backed up and imported idempotently; if another
window owns the import lock, retain the backup and reload after it finishes.

# Debugging guide

Brote uses the Debug Adapter Protocol (DAP). Go through Delve is the supported
backend in this release; other language backends are not included yet.

## Start from your agent

Open your Go project in Pi or Codex and ask Brote to start a debug session. It
can use an existing executable with debugging information or a Go configuration
from `.vscode/launch.json`. Source configurations require `--build` to compile;
launching an existing executable never rebuilds it.

For Pi, `/debug-sessions` lists current runs and their inspector URLs.
`/debug-connect SESSION_ID` attaches the current conversation to an existing run.
Run `/reload` after installing or updating the Pi integration.

Pi installs and updates Brote directly from GitHub:

```sh
pi install git:github.com/javiermolinar/Brote
pi update --extensions
```

The Git package downloads the core for its exact version, including the WebUI,
without compiling anything. Runtime files stay in the versioned
`~/.cache/brote/runtimes/` cache (or `$XDG_CACHE_HOME/brote/runtimes/`). Pi updates
and removal leave that cache intact so existing debug processes keep working.
Use **End run** before manually removing an old cached version.

If npm lifecycle scripts were disabled or a custom Pi package manager omitted
the install hook, the extension prepares the same core when Pi loads it. You can
also run `npm run setup:pi` inside the package checkout to prepare it explicitly.
Normal development `npm ci` skips the download; `npm run setup:pi` opts in.

For Codex, a newly started run binds to the current conversation. To connect an
existing run, use **Attach to agent** in the inspector and paste its prompt into
your Codex conversation. The integration starts an event bridge that delivers
questions and inspector-initiated debugging tasks back to that conversation.

The inspector and agent attach without changing whether the program is running
or paused. If no source is selected yet, open a project file and set a breakpoint.

## Inspect and discuss

In the browser inspector you can:

- Click beside a line number to toggle a breakpoint; right-click for a condition.
- Select a stack frame or goroutine to inspect its locals.
- Evaluate a read-only expression, or pin it to refresh at each pause.
- Select code or click a variable to ask the agent a question beside the source.

Questions capture the pause, source, and values that prompted them. Replies stay
in that discussion, including follow-ups. Saved evidence remains historical when
you step or the run ends; it is not a live view of the target.

Inspection, breakpoints, and watches are shared. You do not need to transfer
ownership between the browser and the agent.

## Let the agent execute

Ask your agent to debug or investigate the program or test. That request authorizes
execution within its scope; you do not need to approve it again in the inspector.
For example:

> Run until `attempt == 3`, inspect `total`, and leave the program paused.

You can also open **Debug with agent** in the inspector, describe the investigation,
and choose **Start debugging** to send it to the connected conversation. Attaching,
viewing state, and asking a debugger comment question remain read-only.

The agent records a task scoped to that instruction and conversation, then sets
breakpoints before executing. It can step or continue while that task is active.
Completing the investigation ends its execution scope. **Stop agent** cancels the task and
requests a pause without ending the run. Human stepping, a changed agent binding,
or a disconnected listener can also revoke the task. The debugger's **Pause**
button takes precedence over agent execution. Execution leases expire if the
agent stops renewing them; cancelled or expired work does not restart automatically.

For CLI agents, the sequence is:

```sh
brote state SESSION_ID --summary
brote task-start SESSION_ID --binding BINDING --revision REVISION --instruction "Debug the retry and leave it paused"
brote task-execute SESSION_ID --task TASK --binding BINDING --operation continue --wait 30s
brote task-complete SESSION_ID --task TASK --binding BINDING
```

Use the fresh state's binding and revision, and the task ID returned by `task-start`.
Keep the same task across steps. The bounded execution command renews its lease;
use `task-heartbeat` between calls while actively investigating. Pi provides the
same flow through `debug_task` (`start`, `claim`, `complete`, `cancel`) and
`debug_execute`. New sessions and Run again start paused. Attachment and recovery
do not request execution.

You can always use the debugger's own execution controls. The old handover and
reclaim commands remain for compatibility; they are not required by this workflow.

## Use the native VS Code debugger

Start your normal Go debugger with **F5** and stop at a breakpoint. Select code,
choose **Ask Brote About Selection**, and type in the inline comment box. Select
an available model when prompted. Answers stream into the thread; use **Continue
in Chat** for a longer conversation.

This mode captures the selected frame through VS Code's active debug adapter.
Its discussions are stored in VS Code, separate from the browser's shared runs.
Use VS Code's normal debugger controls to step and continue.

To use a shared Brote run instead, launch it from `@brote` Chat or choose
**Brote: Attach Session**. Shared discussions are available in both the editor
and browser. A chat request to debug records the investigation with `task-start`;
the tools execute within that task without an additional confirmation dialog.
They keep its scope across steps and use `task-complete` when done. A run's questions are routed to its attached agent;
they are not broadcast to every open frontend.

## Use a VS Code launch configuration

List the profiles in your project's `.vscode/launch.json`, then select one by name:

```sh
brote configs --project "$PWD"
brote start --project "$PWD" --config "Launch server" --build
```

Go `launch` profiles support `debug`, `test`, `auto`, and `exec` modes. The first
three build a session-owned executable with debug symbols when you pass
`--build`; `exec` uses the existing `program` executable and does not accept
`--build`. Every session starts paused, including profiles with
`stopOnEntry: false`. Recovery and **Run again** reuse the executable.

Brote reads JSON comments, trailing commas, platform overrides (`osx`, `linux`,
`windows`), `program`, `cwd`, `args`, `env`, `envFile`, `buildFlags`, and
`dlvToolPath`. Relative program, working-directory and environment-file paths
resolve from `--project`; an omitted `cwd` uses the program's directory.
`envFile` accepts one path or an array of paths: later files override earlier
ones, and `env` overrides the files and inherited environment. An `env` value of
`null` removes the variable. Build flags accept an array or a quoted string;
Brote reserves output, build-directory and debug-symbol flags.

Variables include `${workspaceFolder}`, `${workspaceFolderBasename}`,
`${env:NAME}`, `${userHome}`, and file variables such as `${file}` and
`${fileDirname}`. Pass `--file path/to/main.go` to supply the active file for
file variables or `auto` mode. Use `--launch-file PATH` to read a different
configuration file; that path is relative to the shell's current directory.
If it contains exactly one Go launch profile, its name can be omitted.
Arguments after `--` append to the profile's arguments.

```sh
brote start --config "Launch file" --file cmd/server/main.go --build
brote start --config "Prebuilt server" -- --port 8080
```

This supports local Go launches through Delve. Attach/remote profiles, compound
launches, task hooks, interactive terminals, source-path mappings,
`${command:...}`/`${input:...}` variables and VS Code settings are not imported.
Unsupported attributes or unresolved variables produce an error. Use
`console: "internalConsole"` or omit `console`; output goes to the session's
Delve log. The [VS Code variables reference](https://code.visualstudio.com/docs/reference/variables-reference)
describes the editor's variable syntax.

**Run again** retains the resolved arguments, working directory and environment
overrides even if the original configuration changes. Environment overrides are
stored in a private launch file alongside the saved run, outside the public
session metadata and event stream.

To keep reusable settings for later sessions, create or edit a named profile in
`.vscode/launch.json` directly, or ask your agent to do it. Use
`${workspaceFolder}` for project paths and `envFile` or `${env:NAME}` references
for environment settings. Brote lists and launches those profiles.

## Debug Go tests

Use a Go launch profile with `mode: "test"` to debug a package's tests, including
library packages without a `main` function:

```json
{
  "name": "Package tests",
  "type": "go",
  "request": "launch",
  "mode": "test",
  "program": "${workspaceFolder}/internal/example",
  "args": ["-test.run", "^TestExample$/^subtest$", "-test.v"]
}
```

Add this entry to `configurations` in `.vscode/launch.json`, then launch it:

```sh
brote start --config "Package tests" --build
```

Brote compiles the package with `go test -c` and debug symbols, then launches the
test binary paused. Set breakpoints in production code or `_test.go` files and
inspect test locals as usual. The build itself does not execute the tests.

Omit `-test.run` to run the package's full suite, use `^TestExample$` for one test,
or use `^TestExample$/^subtest$` for one subtest. These are compiled test-binary
arguments, so use `-test.run`, `-test.v`, and `-test.count`, including when adding
arguments after `--`:

```sh
brote start --config "Package tests" --build -- -test.count=1
```

Profiles using `program: "${file}"` with an active `_test.go` file build its whole
package, including sibling files. For an `auto` profile, pass
`--file path/to/example_test.go` to select test mode. Existing test binaries can
also use `mode: "exec"` or `--binary` without `--build`.

## Use a precompiled binary

Use an existing Go executable or test binary with DWARF symbols. Build once using
the project's instructions, commonly with `-gcflags='all=-N -l'`. Changed source
does not change a running binary.

For a manual demo from this checkout, with Brote installed:

```sh
go build -gcflags='all=-N -l' -o /tmp/brote-demo ./examples/demo
brote start --binary /tmp/brote-demo --project "$PWD/examples/demo" --thread ''
```

Open the returned inspector URL, or use the returned session ID in the terminal:

```sh
brote break SESSION_ID --function main.process --condition 'attempt == 3'
brote continue SESSION_ID --human --wait 20s
brote state SESSION_ID --summary
```

`--human` identifies a command you run directly. Agents record the user's debugging
request with `task-start` and use the task
tools instead. Check the fresh location and values before concluding that a
condition fired. Program arguments, including test flags, go after `--` on start.

Useful inspection commands include:

```sh
brote state SESSION_ID --goroutine 1 --frame 0 --summary
brote eval SESSION_ID --expression total --depth 3 --count 64
brote watch SESSION_ID --expression total
brote unwatch SESSION_ID --expression total
```

Evaluation is bounded and read-only; arbitrary function calls, assignments and
channel receives are rejected. `brote doctor` checks debugger and host availability.

## End, reconnect, and recover

Closing the browser or agent does not stop the debugger. Use **End run**, Pi's
`/debug-stop SESSION_ID`, or `brote end-session SESSION_ID --confirmed` to terminate
the debugger and target. Saved investigation history remains available.

An offline agent can reconnect to a live run without restarting it. If the broker
has failed but Delve and the target survive, `brote recover SESSION_ID` reconnects
to that process. Use the returned inspector URL if its endpoint changed.

`brote history` lists saved runs; `brote history SESSION_ID` reads their durable
events without a live process. **Run again** starts a new process using the saved
launch configuration. Saved snapshots are evidence, not time-travel execution.

Source warnings identify possible differences between the executable and files
on disk. An unverified source fingerprint means the match is unknown. Rebuild and
start a new run when you need to debug changed code.

## Development checks

```sh
npm ci
npm run build
npm test
go vet ./...
DH_INTEGRATION=1 CODEX_THREAD_ID='' go test -race ./...
```

The integration tests create disposable Go targets and exercise real Delve
transports, stepping, breakpoints, durable evidence, cancellation, and recovery.
Host notification tests use stubs. Authenticated agent/model responses need a
separate manual check; cross-compilation does not prove native operation on every
release platform.
