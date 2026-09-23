package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type commandHandler func([]string) (any, error)
type command struct {
	name, description, syntax, flags, example string
	hidden                                    bool
	run                                       commandHandler
	children                                  []*command
}

func (c *command) child(name string) *command {
	for _, child := range c.children {
		if child.name == name {
			return child
		}
	}
	return nil
}

// Every route terminates in a handler; aliases never recursively dispatch.
func commandRegistry() *command {
	root := &command{name: "brote", description: "Persistent Go / Delve sessions"}
	add := func(path, description string, run commandHandler) *command {
		c := root
		for _, name := range strings.Fields(path) {
			next := c.child(name)
			if next == nil {
				next = &command{name: name}
				c.children = append(c.children, next)
			}
			c = next
		}
		c.description, c.run = description, run
		return c
	}
	action := func(verb string) commandHandler {
		return func(args []string) (any, error) { return canonicalAction(verb, args) }
	}
	service := func(verb string) commandHandler {
		return func(args []string) (any, error) { return serviceReadCommandMode(verb, args, true) }
	}
	install := func(verb string) commandHandler {
		return func(args []string) (any, error) { return installation(verb, args) }
	}
	definitions := func(kind, operation string) commandHandler {
		return func(args []string) (any, error) {
			return definitionCommandMode(kind, append([]string{operation}, args...), true)
		}
	}
	comments := func(verb string) commandHandler {
		return func(args []string) (any, error) { return commentCommand(append([]string{verb}, args...)) }
	}
	add("session", "Start, discover and manage sessions", nil)
	add("debug", "Inspect and control a debug target", nil)
	add("query", "Read saved traces", nil)
	add("comment", "Read and update discussions", nil)
	add("system", "Install and diagnose Brote", nil)
	add("session start", "Launch a target", objectCommand(start))
	add("session attach", "Attach to an existing process", attachSession)
	add("session list", "List live sessions", sessionList)
	add("session open", "Open the session or workspace UI", openSession)
	add("session stop", "Explicitly terminate a target", endSession)
	for _, verb := range []string{"detach", "bind"} {
		add("session "+verb, "Manage session "+verb, action(verb))
	}
	add("session restart", "Restart this run or launch a saved run", restartSession)
	add("session history", "Read saved session events", sessionHistory)
	add("session configs", "List launch configurations", configsCommand)
	add("session recover", "Recover a session", singleSession(recoverSession))
	add("session cleanup", "Clean up a session", singleSession(cleanupSession))
	add("session events watch", "Stream session events", func(a []string) (any, error) { return eventsCommandMode(a, false, true) })
	add("session events wait", "Wait for a session event", func(a []string) (any, error) { return eventsCommandMode(a, true, true) })
	add("session events acknowledge", "Acknowledge event delivery", action("event-status"))
	add("session events retry", "Retry notification delivery", action("retry-notification"))
	for _, verb := range []string{"state", "eval", "continue", "pause"} {
		add("debug "+verb, "Debug "+verb, action(verb))
	}
	add("debug sources", "Read session sources", sourcesCommand)
	for _, verb := range []string{"capabilities", "goroutines", "stack", "captures"} {
		add("debug "+verb, "Read "+verb, service(verb))
	}
	add("debug watch add", "Watch an expression", action("watch"))
	add("debug watch remove", "Remove a watched expression", action("unwatch"))
	add("debug step", "Step over by default; optionally step into or out", action("canonical-step"))
	for _, kind := range []string{"breakpoint", "tracepoint"} {
		for _, verb := range []string{"list", "add", "update", "remove"} {
			add("debug "+kind+" "+verb, verb+" "+kind, definitions(kind, verb))
		}
	}
	for _, verb := range []string{"start", "heartbeat", "complete", "cancel"} {
		add("debug task "+verb, verb+" an authorized task", action("task-"+verb))
	}
	add("debug task execute", "Execute within a task", taskExecute)
	add("query traces", "List or retrieve saved traces", queryTraces)
	for _, verb := range []string{"list", "create", "ask", "reply", "retry", "delivery", "index", "import"} {
		add("comment "+verb, verb+" discussion", comments(verb))
	}
	add("comment status", "Open or resolve a discussion", commentStatus)
	add("system setup", "Install or repair chosen components", func(a []string) (any, error) { return installationCommand("setup", a, true) })
	add("system status", "Read installation status", func(a []string) (any, error) { return installationCommand("installation", a, true) })
	add("system uninstall", "Remove a chosen component", func(a []string) (any, error) { return installationCommand("uninstall", a, true) })
	add("system doctor", "Diagnose the environment", objectCommand(canonicalDoctor))
	add("system version", "Read version and capabilities", noArguments(versionCommand))

	// Compatibility routes preserve the old operation values and parsing contracts.
	for _, verb := range []string{"state", "eval", "watch", "unwatch", "bind", "continue", "next", "step", "stepout", "pause", "break", "clear", "handover", "reclaim", "restart", "detach", "stop", "event-status", "retry-notification", "task-start", "task-heartbeat", "task-complete", "task-cancel", "task-authorize", "task-delivery", "editor-error", "consumer-challenge", "consumer-close", "consumer-fact", "consumer-next"} {
		add(verb, "Compatibility action", func(a []string) (any, error) { return actionCommand(verb, a) }).hidden = true
	}
	for _, verb := range []string{"captures", "capabilities", "goroutines", "stack"} {
		add(verb, "Compatibility inspection", func(a []string) (any, error) { return serviceReadCommand(verb, a) }).hidden = true
	}
	for _, kind := range []string{"breakpoint", "tracepoint"} {
		add(kind, "Compatibility definitions", func(a []string) (any, error) { return definitionCommand(kind, a) }).hidden = true
	}
	legacy := map[string]commandHandler{
		"start": objectCommand(start), "configs": configsCommand, "ui": openWorkspace, "workspace": workspaceCommand, "saved-run": savedRunCommand, "run-again": runAgainCommand,
		"history": historyCommand, "sources": sourcesCommand, "end-session": endSession, "task-execute": taskExecute, "version": versionCommand,
		"events": func(a []string) (any, error) { return eventsCommand(a, false) }, "await-control": func(a []string) (any, error) { return eventsCommand(a, true) },
		"setup": install("setup"), "installation": install("installation"), "repair": install("repair"), "uninstall": install("uninstall"), "doctor": objectCommand(doctor),
		"recover": singleSession(recoverSession), "cleanup": singleSession(cleanupSession), "sessions": listSessions,
		"traces": traceRecords, "trace": traceQuery,
		"dap": func(a []string) (any, error) { return nil, runDAP(a) }, "bridge": func(a []string) (any, error) { return nil, bridge(a) },
		"serve": func(a []string) (any, error) { return nil, serve(a) }, "trace-serve": traceServe, "trace-service": traceService, "ui-serve": func([]string) (any, error) { return nil, serveUI() },
	}
	// Map iteration cannot affect help or registry inventory ordering.
	keys := make([]string, 0, len(legacy))
	for name := range legacy {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		add(name, "Compatibility or internal entry point", legacy[name]).hidden = true
	}
	for _, verb := range []string{"resolve", "reopen", "claim", "answer-failed", "continue-thread"} {
		add("comment "+verb, "Compatibility discussion", comments(verb)).hidden = true
	}
	describeCommands(root)
	return root
}

func Run(args []string) (any, error) { return runCLI(args, os.Stdout) }
func runCLI(args []string, output io.Writer) (any, error) {
	c, path, rest, help, err := resolveCommand(commandRegistry(), args)
	if err != nil {
		return nil, err
	}
	if help || c.run == nil {
		if !help && len(rest) > 0 {
			return nil, fmt.Errorf("unknown command: %s %s", path, rest[0])
		}
		if !help && path != "brote" {
			return nil, fmt.Errorf("%s: subcommand required (use --help)", path)
		}
		_, err = fmt.Fprint(output, commandHelp(c, path))
		return nil, err
	}
	result, err := c.run(rest)
	if err != nil {
		return result, fmt.Errorf("%s: %w", path, err)
	}
	return result, nil
}
func resolveCommand(root *command, args []string) (*command, string, []string, bool, error) {
	c, path, help := root, "brote", false
	if len(args) > 0 && args[0] == "help" {
		help = true
		args = args[1:]
	}
	for len(c.children) > 0 && len(args) > 0 {
		if args[0] == "--help" || args[0] == "-h" {
			break
		}
		next := c.child(args[0])
		if next == nil {
			return nil, path, nil, false, fmt.Errorf("unknown command: %s %s", path, args[0])
		}
		c = next
		path += " " + args[0]
		args = args[1:]
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" {
			help = true
		}
	}
	if c == root && len(args) == 0 {
		help = true
	}
	return c, path, args, help, nil
}
func commandHelp(c *command, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", path, c.description)
	if c.run != nil {
		fmt.Fprintf(&b, "\nUsage: %s %s\n", path, c.syntax)
	}
	for _, child := range c.children {
		if !child.hidden {
			fmt.Fprintf(&b, "  %-16s %s\n", child.name, child.description)
		}
	}
	if c.flags != "" {
		fmt.Fprintf(&b, "\nOptions:\n%s\n", c.flags)
	}
	if c.example != "" {
		fmt.Fprintf(&b, "\nExample: %s\n", c.example)
	}
	fmt.Fprintln(&b, "\nUse --help at any command level.")
	if path == "brote" {
		fmt.Fprintln(&b, "Usage examples: see docs/debugging.md and docs/tracing.md.")
	}
	return b.String()
}

func objectCommand(fn func([]string) (obj, error)) commandHandler {
	return func(args []string) (any, error) { return fn(args) }
}

func noArguments(run commandHandler) commandHandler {
	return func(args []string) (any, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("unexpected arguments")
		}
		return run(args)
	}
}
