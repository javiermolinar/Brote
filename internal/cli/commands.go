package cli

import (
	"context"
	"flag"
	"fmt"
	"time"

	"agentdebugger/internal/editors/vscode"
	"agentdebugger/internal/session"
	"agentdebugger/internal/tracing"
)

type obj = map[string]any

func usage() string {
	return `Brote — persistent Go / Delve sessions

  brote start --binary PATH --project DIR [--title TITLE] [--investigation ID] [--no-ui] -- [program args]
  brote start --pid PID --binary PATH --project DIR  (attach an existing process)
  brote start --legacy --binary PATH [--backend rpc]  (explicit legacy/Zed compatibility)
  brote configs [--project DIR] [--launch-file PATH]
  brote start --config NAME [--project DIR] [--launch-file PATH] [--file PATH] [--build] -- [extra args]
  brote ui  (persistent investigation workspace)
  brote run-again ID  (new run with saved executable and arguments)
  brote setup --agent codex|pi [--editor vscode]
  brote events ID --managed --consumer NAME --binding ID  (Go-managed delivery and host facts)
  brote events ID [--cursor N] [--binding ID]  (JSONL stream)
  brote await-control ID [--cursor N] [--timeout 20s]
  brote event-status ID --event N --revision N --status acknowledged
  brote end-session ID --confirmed  (explicit human termination)
  brote comment list SESSION
  brote comment create SESSION --file PATH --line N --body-file PATH [--recipient-kind agent|provider --recipient-id ID]
  brote comment ask SESSION THREAD --body-file PATH --context original|current
  brote comment delivery SESSION THREAD --question ID --binding ID --revision N --attempt ID --status thinking
  brote comment reply SESSION THREAD --question ID --binding ID --revision N --attempt ID --body-file PATH --message-id KEY
  brote comment retry|resolve|reopen SESSION THREAD [--offline]
  brote comment index WORKSPACE | comment import WORKSPACE --body-file PATH
  brote task-start ID --binding BINDING --revision N --instruction "Investigate the retries"
  brote task-execute ID --task TASK_ID --binding BINDING --operation next [--wait 30s]
  brote task-heartbeat|task-complete ID --task TASK_ID --binding BINDING
  brote task-cancel ID --human
  brote dap ID  (authenticated editor DAP transport over stdio)
  brote tracepoint add SESSION --file PATH --line N --name LABEL --values '{"alias":"expression"}'
  brote tracepoint update SESSION --id POINT --revision N [--enabled=false] [--capture-limit 100]
  brote tracepoint remove SESSION --id POINT --revision N
  brote tracepoint list SESSION  (breakpoint accepts the same CRUD commands)
  brote breakpoint add SESSION --function main.work
  brote captures|capabilities SESSION
  brote goroutines SESSION [--start N] [--count 64]
  brote stack SESSION [--goroutine N] [--start N] [--count 64]
  brote traces  (saved trace IDs and export status for all adapters)
  brote trace TRACE_ID  (stored Tempo trace JSON)
  brote sessions
  brote history [ID]  (saved sessions or ordered events, works offline)
  brote state ID [--goroutine N] [--frame N] [--summary]
  brote eval ID --expression EXPR [--goroutine N] [--frame N] [--depth 3] [--count 64]
  brote watch|unwatch ID --expression EXPR
  brote bind ID --thread UUID
  brote bind ID --binding CLIENT_ID --name DISPLAY_NAME
  brote installation|repair
  brote uninstall --component codex|pi|vscode|core
  brote retry-notification ID
  brote recover ID
  brote cleanup ID
  brote doctor [--binary PATH] [--project DIR]
  brote break ID --file PATH --line N [--condition EXPR] [--hit-condition '== 3']
  brote break ID --function main.process [--condition EXPR]
  brote clear ID --breakpoint N
  brote continue|next|step|stepout ID [--wait 20s] [--summary]
  brote pause ID
  brote handover ID [--editor browser|zed|vscode] [--no-open]
  brote reclaim ID
  brote restart|detach ID --human  (shared-service sessions)
  brote stop ID

New starts use the shared service. --legacy or --service=false opts out; run-again
retains the stored launch mode. Shared sessions reject direct Zed/RPC handover.
Discussion recipients never acquire execution authority. --offline writes saved
history without starting a debugger; current context requires a live pause.
All commands print JSON. Source launch configurations require --build; --binary
and exec configurations never compile the target. Closing Zed or the panel
does not stop the program. A user's debugging request authorizes that task;
record its scope with task-start before execution. Attach and comment questions
stay read-only. --human is for direct human operations. end-session explicitly terminates a run.`
}

func Run(args []string) (any, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println(usage())
		return nil, nil
	}
	verb := args[0]
	if verb == "tracepoint" || verb == "breakpoint" {
		return definitionCommand(verb, args[1:])
	}
	if verb == "captures" || verb == "capabilities" || verb == "goroutines" || verb == "stack" {
		return serviceReadCommand(verb, args[1:])
	}
	if verb == "dap" {
		return nil, runDAP(args[1:])
	}
	if verb == "trace-serve" {
		return nil, tracing.Serve()
	}
	if verb == "trace-service" || verb == "traces" || verb == "trace" {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		switch verb {
		case "trace-service":
			endpoint, err := tracing.Ensure(ctx)
			return obj{"endpoint": endpoint}, err
		case "traces":
			return tracing.Records(ctx)
		case "trace":
			if len(args) != 2 {
				return nil, fmt.Errorf("usage: trace TRACE_ID")
			}
			return tracing.Query(ctx, args[1])
		}
	}
	if verb == "configs" {
		f := flag.NewFlagSet("configs", flag.ContinueOnError)
		project := f.String("project", ".", "Project/source directory")
		path := f.String("launch-file", "", "VS Code launch.json path")
		if err := f.Parse(args[1:]); err != nil {
			return nil, err
		}
		if f.NArg() != 0 {
			return nil, fmt.Errorf("usage: configs [--project DIR] [--launch-file PATH]")
		}
		return vscode.List(*project, *path)
	}
	if verb == "ui-serve" {
		return nil, serveUI()
	}
	if verb == "ui" {
		endpoint, e := ensureUI()
		return obj{"panel": endpoint}, e
	}
	if verb == "workspace" {
		return session.Workspace(context.Background())
	}
	if verb == "saved-run" && len(args) == 2 {
		return session.SavedRun(args[1])
	}
	if verb == "run-again" && len(args) == 2 {
		histories, err := session.ListHistory()
		if err != nil {
			return nil, err
		}
		for _, h := range histories {
			if h.ID == args[1] {
				group, err := session.InvestigationFor(h.ID)
				if err != nil {
					return nil, err
				}
				argv := []string{"--binary", h.Binary, "--project", h.Project, "--investigation", group, "--thread", "", "--"}
				argv = append(argv, h.Args...)
				settings, err := session.ReadLaunchSettings(h.Directory)
				if err != nil {
					return nil, err
				}
				return startWithLaunch(argv, settings)
			}
		}
		return nil, fmt.Errorf("saved launch configuration not found")
	}
	if verb == "history" {
		if len(args) == 1 {
			return session.ListHistory()
		}
		if len(args) == 2 {
			return session.ReadHistory(args[1])
		}
		return nil, fmt.Errorf("usage: history [ID]")
	}
	if verb == "comment" {
		return commentCommand(args[1:])
	}
	if verb == "sources" {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("usage: sources ID [FILE]")
		}
		s, err := session.Read(args[1])
		if err != nil {
			return nil, err
		}
		file := ""
		if len(args) == 3 {
			file = args[2]
		}
		return session.Sources(s, file)
	}
	if verb == "end-session" {
		if len(args) != 3 || args[2] != "--confirmed" {
			return nil, fmt.Errorf("usage: end-session ID --confirmed (terminates the target, regardless of owner)")
		}
		return session.End(context.Background(), args[1])
	}
	if verb == "task-execute" {
		return taskExecute(args[1:])
	}
	if verb == "version" {
		return obj{"version": Version, "protocol": 2, "capabilities": []string{"executionTasks", "taskStart", "taskDelivery", "taskExecute", "embeddedWebUI", "sharedServiceV1", "tracepoints", "sessionOTLP", "vscodeF5"}}, nil
	}
	if verb == "events" || verb == "await-control" {
		return eventsCommand(args[1:], verb == "await-control")
	}
	if verb == "bridge" {
		return nil, bridge(args[1:])
	}
	if verb == "setup" || verb == "installation" || verb == "repair" || verb == "uninstall" {
		return installation(verb, args[1:])
	}
	if verb == "start" {
		return start(args[1:])
	}
	if verb == "serve" {
		return nil, serve(args[1:])
	}
	if verb == "doctor" {
		return doctor(args[1:])
	}
	if (verb == "recover" || verb == "cleanup") && len(args) == 2 {
		if verb == "recover" {
			return recoverSession(args[1])
		}
		return cleanupSession(args[1])
	}
	if verb == "sessions" {
		return session.List(context.Background())
	}

	if len(args) < 2 {
		return nil, fmt.Errorf("session ID required\n%s", usage())
	}
	s, e := session.Read(args[1])
	if e != nil {
		return nil, e
	}
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	commandID := f.String("command-id", "", "stable execution request identity for retry detection")
	consumer := f.String("consumer", "", "managed host consumer")
	instance := f.String("instance", "", "managed host instance")
	turn := f.String("turn", "", "active host turn")
	task := f.String("task", "", "current debugging task ID")
	humanAction := f.Bool("human", false, "direct human debugger action or authorization")
	instruction := f.String("instruction", "", "user-requested investigation scope")
	file := f.String("file", "", "source path")
	line := f.Int("line", 0, "line")
	fn := f.String("function", "", "function name")
	cond := f.String("condition", "", "condition")
	hit := f.String("hit-condition", "", "hit condition")
	bp := f.Int("breakpoint", 0, "breakpoint ID")
	gid := f.Int("goroutine", 0, "goroutine ID")
	frame := f.Int("frame", 0, "frame index")
	brief := f.Bool("brief", false, "state metadata without debugger inspection")
	summary := f.Bool("summary", false, "compact stack and selected-frame values")
	wait := f.Duration("wait", 0, "wait for pause")
	noOpen := f.Bool("no-open", false, "do not open the editor")
	editor := f.String("editor", "", "handover: browser, zed or vscode (defaults to previous frontend)")
	expr := f.String("expression", "", "read-only Go expression")
	depth := f.Int("depth", 3, "variable depth (0–6)")
	count := f.Int("count", 64, "maximum array/struct entries (1–128)")
	thread := f.String("thread", "", "Codex task UUID; empty disables wakeups")
	binding := f.String("binding", "", "client binding ID")
	name := f.String("name", "Agent", "agent display name")
	note := f.String("note", "", "handover note")
	attempt := f.String("attempt", "", "current delivery attempt")
	event := f.String("event", "", "event ID")
	delivery := f.String("status", "acknowledged", "event delivery status")
	revision := f.Uint64("revision", 0, "binding revision")
	notify := f.Bool("notify", false, "legacy flag; handback always emits an event")
	if e = f.Parse(args[2:]); e != nil {
		return nil, e
	}
	if len(f.Args()) > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	if verb == "task-start" && (*humanAction || *binding == "" || *revision == 0 || *task != "") {
		return nil, fmt.Errorf("usage: task-start ID --binding BINDING --revision N --instruction REQUEST (record the user's debugging request as the agent)")
	}
	if verb == "state" {
		v, err := api(s, "GET", fmt.Sprintf("/api/state?goroutine=%d&frame=%d&brief=%d", *gid, *frame, map[bool]int{true: 1, false: 0}[*brief]), nil)
		if *summary && err == nil {
			v = summarizeState(v)
		}
		return v, err
	}
	state, e := api(s, "GET", "/api/state?brief=1", nil)
	if e != nil {
		return nil, e
	}
	if *binding == "" && s.Binding != nil {
		*binding = s.Binding.ID
	}
	body := obj{"attempt": *attempt, "binding": *binding, "actor": "agent", "name": *name, "note": *note, "event": *event, "status": *delivery, "revision": *revision, "action": verb, "generation": state["generation"], "file": *file, "line": *line, "function": *fn, "condition": *cond, "hitCondition": *hit, "breakpoint": *bp, "open": !*noOpen}
	body["consumer"], body["instance"], body["turn"] = *consumer, *instance, *turn
	body["commandId"] = *commandID
	body["error"] = *note
	body["task"], body["instruction"] = *task, *instruction
	if *humanAction {
		body["actor"] = "human"
	}
	body["editor"] = *editor
	body["expression"], body["depth"], body["count"], body["goroutine"], body["frame"], body["thread"], body["notify"] = *expr, *depth, *count, *gid, *frame, *thread, *notify
	if verb == "bind" && *thread != "" {
		if !validCodexThread(*thread) {
			return nil, fmt.Errorf("invalid Codex thread UUID")
		}
		if *binding == "" {
			*binding = session.NewID(16)
		}
		body["binding"] = *binding
		body["name"] = "Codex"
	}
	res, e := api(s, "POST", "/api/action", body)
	if e != nil {
		return nil, e
	}
	if verb == "bind" && *thread != "" {
		fresh, err := session.Read(s.ID)
		if err != nil {
			return nil, err
		}
		if err = configureBridge(fresh, *thread); err != nil {
			return res, err
		}
	}
	if *wait > 0 {
		for deadline := time.Now().Add(*wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			v, e := api(s, "GET", "/api/state?brief=1", nil)
			if e != nil {
				return nil, e
			}
			if str(v["status"]) != "running" {
				if pending, ok := v["capturePending"].(float64); ok && pending > 0 {
					continue
				}
				v, e = api(s, "GET", "/api/state", nil)
				if e != nil {
					return nil, e
				}
				if *summary {
					v = summarizeState(v)
				}
				return v, nil
			}
		}
		return nil, fmt.Errorf("wait timed out; session remains alive (use state or pause)")
	}
	return res, nil
}
