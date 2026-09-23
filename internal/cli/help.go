package cli

import (
	"bytes"
	"fmt"
	"strings"
)

// Help is metadata on the same nodes used for dispatch. Shared action options
// are rendered from their actual parser; definitions share their CRUD policies.
func describeCommands(root *command) {
	set := func(path, syntax, flags, example string) *command {
		c := root
		for _, part := range strings.Fields(path) {
			c = c.child(part)
			if c == nil {
				panic("help for missing route: " + path)
			}
		}
		c.syntax, c.flags, c.example = syntax, flags, "brote "+path
		if example != "" {
			c.example += " " + example
		}
		return c
	}
	action := func(path, verb, example string) {
		f := actionFlagSet(verb, false)
		var b bytes.Buffer
		f.SetOutput(&b)
		f.PrintDefaults()
		set(path, "SESSION [options]", strings.TrimSuffix(b.String(), "\n"), example)
	}
	launchFlags := `  --binary PATH        Existing executable (required for attach)
  --project DIR        Source directory (default .)
  --config NAME        VS Code launch configuration
  --launch-file PATH   Custom launch.json
  --file PATH          Active file for launch variables
  --build              Build a source configuration
  --dlv PATH           Delve executable
  --title TEXT         Investigation title
  --investigation ID   Existing investigation
  --binding ID         Agent binding
  --name NAME          Agent display name
  --thread UUID        Codex task; empty disables wakeups
  --no-ui              Return broker URL without workspace startup
  --legacy             Legacy session mode
  --service=false      Legacy session mode
  --backend dap|rpc    Debug backend (RPC is legacy)
  --pid PID            Attach instead of launch (binary identity required)
  --editor-start       Require editor configuration within 30s`
	set("session start", "--binary PATH [options] -- [program arguments]", launchFlags, "--binary ./app --project . --no-ui -- --config app.json")
	set("session attach", "--pid PID --binary PATH [--project DIR] [options]", `  --pid PID            Positive PID of an existing process
  --binary PATH        Executable for source identity
  --project DIR        Source directory
  --dlv PATH           Delve executable
  --binding ID         Agent binding
  --name NAME          Agent display name
  --thread UUID        Codex task; empty disables wakeups
  --title TEXT         Investigation title
  --investigation ID   Existing investigation
  --no-ui              Return broker URL
Attach requires shared service mode and accepts no build or program arguments.`, "--pid 1234 --binary ./app --project .")
	set("session list", "[--all]", "  --all                Include offline and saved sessions", "--all")
	set("session open", "[SESSION]", "Returns the session UI URL, or the persistent workspace URL without an ID. Opening a UI does not transfer execution control.", "SESSION")
	set("session stop", "SESSION --confirmed", "  --confirmed          Explicitly terminate the target, regardless of owner", "SESSION --confirmed")
	action("session restart", "restart", "SESSION --human")
	action("session detach", "detach", "SESSION --human")
	action("session bind", "bind", "SESSION --binding client --name Agent")
	set("session history", "SESSION", "Reads ordered saved events without starting a debugger.", "SESSION")
	set("session configs", "[--project DIR] [--launch-file PATH]", "  --project DIR        Source directory\n  --launch-file PATH   Custom launch.json", "--project .")
	set("session recover", "SESSION", "Reconnect a recoverable session using existing recovery checks.", "SESSION")
	set("session cleanup", "SESSION", "Remove stale runtime state using existing lifecycle checks.", "SESSION")
	eventFlags := "  --cursor N           Last event ID\n  --binding ID         Expected binding"
	set("session events watch", "SESSION [options]", eventFlags+"\n  --managed            Managed delivery over NDJSON\n  --consumer NAME      Stable host consumer\nManaged delivery uses stored claims; --cursor applies only to raw event watch.", "SESSION --managed --consumer agent --binding client")
	set("session events wait", "SESSION [options]", eventFlags+"\n  --timeout DURATION   Maximum wait (default 20s; >0 and <=1m)", "SESSION --cursor 0 --timeout 20s")
	action("session events acknowledge", "event-status", "SESSION --event 1 --revision 1 --status acknowledged")
	action("session events retry", "retry-notification", "SESSION")
	for _, verb := range []string{"state", "eval", "continue", "pause"} {
		example := "SESSION"
		if verb == "eval" {
			example += " --expression value"
		}
		if verb == "continue" {
			example += " --task TASK --wait 20s"
		}
		if verb == "pause" {
			example += " --human"
		}
		action("debug "+verb, verb, example)
	}
	action("debug step", "canonical-step", "SESSION --over --task TASK --wait 20s")
	action("debug watch add", "watch", "SESSION --expression value")
	action("debug watch remove", "unwatch", "SESSION --expression value")
	set("debug sources", "SESSION [FILE]", "Read recorded source locations or one source file.", "SESSION")
	for _, verb := range []string{"capabilities", "captures", "goroutines", "stack"} {
		set("debug "+verb, "SESSION [options]", namedFlags(serviceFlagNames(verb)), "SESSION")
	}
	for _, kind := range []string{"breakpoint", "tracepoint"} {
		for _, verb := range []string{"list", "add", "update", "remove"} {
			example := "SESSION"
			switch verb {
			case "add":
				example += " --file main.go --line 20"
			case "update":
				example += " --id POINT --revision 1 --enabled=false"
			case "remove":
				example += " --id POINT --revision 1"
			}
			set("debug "+kind+" "+verb, "SESSION [options]", namedFlags(definitionFlagNames(kind, verb)), example)
		}
	}
	for _, verb := range []string{"start", "heartbeat", "complete", "cancel"} {
		example := "SESSION --task TASK --binding client"
		if verb == "start" {
			example = "SESSION --binding client --revision 1 --instruction 'Investigate this failure'"
		}
		if verb == "cancel" {
			example = "SESSION --human"
		}
		action("debug task "+verb, "task-"+verb, example)
	}
	set("debug task execute", "SESSION --task TASK --binding BINDING --operation OP [--wait DURATION]", `  --operation OP       continue, next, step, stepout or pause (protocol values)
  --task TASK          Current task ID
  --binding BINDING    Current conversation binding
  --wait DURATION     Maximum execution duration, >0 and <=5m (default 30s)`, "SESSION --task TASK --binding client --operation next --wait 20s")
	set("query traces", "[TRACE_ID | --session SESSION]", "  --session SESSION    Filter by stored session identity\nReads saved traces after target exit. Local trace storage may start to serve a query; a debugger is never started.", "--session SESSION")
	for _, verb := range []string{"list", "create", "ask", "reply", "retry", "delivery", "index", "import"} {
		syntax, example := "SESSION THREAD [options]", "SESSION THREAD"
		switch verb {
		case "list":
			syntax, example = "SESSION", "SESSION"
		case "create":
			syntax, example = "SESSION [options]", "SESSION --file main.go --line 20 --body-file question.txt"
		case "index":
			syntax, example = "WORKSPACE", "/path/to/project"
		case "import":
			syntax, example = "WORKSPACE --body-file PATH", "/path/to/project --body-file comments.json"
		case "ask":
			example += " --body-file question.txt --context original"
		case "reply":
			example += " --question QUESTION --binding client --revision 1 --attempt ATTEMPT --body-file answer.txt --message-id KEY"
		case "delivery":
			example += " --question QUESTION --binding client --revision 1 --attempt ATTEMPT --status thinking"
		}
		set("comment "+verb, syntax, namedFlags(commentFlagNames(verb)), example)
	}
	set("comment status", "SESSION THREAD open|resolved [--offline]", "  --offline            Write saved discussion without contacting target", "SESSION THREAD resolved --offline")
	set("system setup", "[--agent codex|pi] [--editor vscode] [--bundle DIR]", "  --agent codex|pi     Install/repair selected agent\n  --editor vscode      Install/repair editor\n  --bundle DIR         Extracted release\nWithout component options, repair recorded installation choices. First setup requires a component selection.", "--agent codex --editor vscode")
	set("system status", "", "Read installation status without changing it.", "")
	set("system uninstall", "--component codex|pi|vscode|core", "  --component NAME     Component to remove", "--component pi")
	set("system doctor", "[--binary PATH] [--project DIR]", "  --binary PATH        Optional target executable\n  --project DIR        Source directory", "--binary ./app --project .")
	set("system version", "", "Read version, protocol and capabilities.", "")
}
func namedFlags(names string) string {
	descriptions := map[string]string{
		"client": "NAME       Configuration owner (default cli)", "id": "ID            Definition ID", "revision": "N       Expected revision", "file": "PATH          Source file", "line": "N             Source line", "function": "EXPR     Function breakpoint", "condition": "EXPR    Read-only condition", "hit-condition": "EXPR  Delve hit condition", "enabled": "BOOL     Enable definition (default true)", "scope": "session|run  Definition scope", "name": "TEXT          Capture label", "values": "JSON        Map of aliases to expressions", "capture-limit": "N  Capture limit (default 100)", "start": "N           Page offset", "count": "N           Page size (default 64)", "goroutine": "N       Goroutine ID", "frame": "N           Frame index", "workspace": "PATH    Workspace index", "offline": "           Use saved history without target access", "context": "original|current  Evidence context", "previous-session": "ID  Earlier investigation run", "body-file": "PATH    UTF-8 body file", "question": "ID       Question identity", "binding": "ID        Binding identity", "attempt": "ID        Delivery attempt", "message-id": "KEY   Idempotent reply identity", "status": "STATE      Delivery status", "error": "TEXT        Failure detail", "run": "ID             Expected execution run", "generation": "N     Expected pause generation", "expression": "EXPR  Variable expression", "recipient-kind": "agent|provider  Recipient kind", "recipient-id": "ID  Recipient identity", "recipient-revision": "N  Recipient revision", "recipient-name": "TEXT  Display name",
	}
	var b strings.Builder
	for _, name := range strings.Fields(names) {
		fmt.Fprintf(&b, "  --%-18s %s\n", name, descriptions[name])
	}
	return strings.TrimSuffix(b.String(), "\n")
}
