# Debugging guide

Brote uses the Debug Adapter Protocol (DAP). Go through Delve is the supported
backend in this release; other language backends are not included yet.

## Start from your agent

Open your Go project in Pi or Codex and ask Brote to start a debug session. It
uses an existing executable with debugging information. If one is needed, ask
your agent to build it first; starting a session never compiles the target.

For Pi, `/debug-sessions` lists current runs and their inspector URLs.
`/debug-connect SESSION_ID` attaches the current conversation to an existing run.
Run `/reload` after installing or updating the Pi integration.

For Codex, a newly started run binds to the current conversation. To connect an
existing run, use **Attach to agent** in the inspector and paste its prompt into
your Codex conversation. The integration starts an event bridge that delivers
questions and authorized tasks back to that conversation.

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

A question does not authorize stepping or continuing. Open **Debug with agent**,
describe the investigation, and choose **Authorize debugging**. For example:

> Run until `attempt == 3`, inspect `total`, and leave the program paused.

The agent receives a task scoped to that instruction and conversation. It can
step or continue while that task is active. **Stop agent** cancels the task and
requests a pause without ending the run. Human stepping, a changed agent binding,
or a disconnected listener can also revoke the task. Execution leases expire if
the agent stops renewing them.

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
and browser. Execution tools request confirmation through VS Code and use the
same scoped task contract. A run's questions are routed to its attached agent;
they are not broadcast to every open frontend.

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

`--human` identifies a command you run directly. Agents use the authorized task
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
