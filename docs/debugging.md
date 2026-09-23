# Debugging examples

Install Brote and Delve using the [README](../README.md#install).
Replace `SESSION` with the session ID returned by Brote. Use `brote --help` or
`brote debug step --help` for command options.

## Ask your agent

In Codex or Pi, open your Go project and ask:

> Start a Brote session for this project. Stop at `main.process`, inspect the
> retry counter, and leave it paused. Give me the inspector URL.

Open the URL, select code or a variable, and ask a question beside it. In Pi,
`/debug-sessions` lists sessions and `/debug-connect SESSION` connects the chat.
In VS Code, start a Brote configuration with **F5**, then select code and choose
**Ask Brote About Selection**.

## Debug the demo from this checkout

```sh
go build -gcflags='all=-N -l' -o /tmp/brote-demo ./examples/demo
brote session start --binary /tmp/brote-demo --project "$PWD/examples/demo" --thread ''
```

The program starts paused. Open the returned URL, or use the terminal:

```sh
brote debug breakpoint add SESSION --function main.process --condition 'attempt == 3'
brote debug continue SESSION --human --wait 20s
brote debug state SESSION --summary
brote debug eval SESSION --expression total
brote debug step SESSION --over --human --wait 20s
brote debug watch add SESSION --expression total
```

`--human` is for commands you run yourself. Agents use the task commands below.
Use `--into` or `--out` instead of `--over` to change the step mode.

## Launch a configured program or test

Add a profile to `.vscode/launch.json`:

```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "Package tests",
      "type": "go",
      "request": "launch",
      "mode": "test",
      "program": "${workspaceFolder}/internal/example",
      "args": ["-test.run", "^TestExample$", "-test.v"]
    }
  ]
}
```

Replace the package and test name, then build and launch:

```sh
brote session configs --project "$PWD"
brote session start --config "Package tests" --build
```

For a program, use `mode: "debug"` and its source directory. For an existing
executable, use `mode: "exec"` and omit `--build`. Append program arguments after
`--`, for example `brote session start --config "Package tests" --build -- -test.count=1`.

## Attach to a running process

```sh
brote session attach --pid 1234 --binary /absolute/path/app --project "$PWD"
brote session open SESSION
brote session detach SESSION --human
```

Use the process's executable. Detach leaves an attached process alive;
`session stop SESSION --confirmed` terminates it.

## Let an agent execute a bounded task

After the user requests an investigation, read the current binding and revision
from state, then use the task ID returned by `task start`:

```sh
brote debug state SESSION --summary
brote debug task start SESSION --binding BINDING --revision REVISION --instruction "Inspect the retry and leave it paused"
brote debug task execute SESSION --task TASK --binding BINDING --operation continue --wait 30s
brote debug task complete SESSION --task TASK --binding BINDING
```

Keep the same task across steps; use `debug task heartbeat` while inspecting
between execution calls. Human Pause takes precedence over agent execution.

## Stop, recover, or rerun

```sh
brote session list
brote debug pause SESSION --human
brote session recover SESSION
brote session stop SESSION --confirmed
brote session list --all
brote session history SESSION
brote session restart SESSION --new-session
```

These are separate actions: recover reconnects after a broker failure when the
target survives; stop terminates the target; rerun starts a new paused process
with the saved configuration. Rerun reuses the executable, so rebuild after source
changes. Closing the browser does not stop a session.

For trace captures and saved data, see [tracing examples](tracing.md).
