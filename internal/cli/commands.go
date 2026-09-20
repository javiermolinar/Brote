package cli

import (
	"context"
	"flag"
	"fmt"
	"time"

	"debug-handover/internal/session"
)

type obj = map[string]any

func usage() string {
	return `Debug Handover — persistent Go / Delve sessions

  debug-handover start --binary PATH --project DIR [--dlv PATH] -- [program args]
  delve-llm-adapter setup --agent codex|pi [--editor vscode]
  debug-handover events ID [--cursor N] [--binding ID]  (JSONL stream)
  debug-handover await-control ID [--cursor N] [--timeout 20s]
  debug-handover event-status ID --event N --revision N --status acknowledged
  debug-handover end-session ID --confirmed  (explicit human termination)
  debug-handover sessions
  debug-handover state ID [--goroutine N] [--frame N] [--summary]
  debug-handover eval ID --expression EXPR [--goroutine N] [--frame N] [--depth 3] [--count 64]
  debug-handover watch|unwatch ID --expression EXPR
  debug-handover bind ID --thread UUID
  debug-handover bind ID --binding CLIENT_ID --name DISPLAY_NAME
  delve-llm-adapter installation|repair
  delve-llm-adapter uninstall --component codex|pi|vscode|core
  debug-handover retry-notification ID
  debug-handover recover ID
  debug-handover cleanup ID
  debug-handover doctor [--binary PATH] [--project DIR]
  debug-handover break ID --file PATH --line N [--condition EXPR] [--hit-condition '== 3']
  debug-handover break ID --function main.process [--condition EXPR]
  debug-handover clear ID --breakpoint N
  debug-handover continue|next|step|stepout ID [--wait 20s] [--summary]
  debug-handover pause ID
  debug-handover handover ID [--editor browser|zed|vscode] [--no-open]
  debug-handover reclaim ID
  debug-handover stop ID

All commands print JSON. start never compiles the target. Closing Zed or the panel
does not stop the program. stop explicitly terminates the owned debug session.`
}

func Run(args []string) (any, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println(usage())
		return nil, nil
	}
	verb := args[0]
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
	if verb == "version" {
		return obj{"version": Version, "protocol": 2}, nil
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
	file := f.String("file", "", "source path")
	line := f.Int("line", 0, "line")
	fn := f.String("function", "", "function name")
	cond := f.String("condition", "", "condition")
	hit := f.String("hit-condition", "", "hit condition")
	bp := f.Int("breakpoint", 0, "breakpoint ID")
	gid := f.Int("goroutine", 0, "goroutine ID")
	frame := f.Int("frame", 0, "frame index")
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
	if verb == "state" {
		v, err := api(s, "GET", fmt.Sprintf("/api/state?goroutine=%d&frame=%d", *gid, *frame), nil)
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
	body := obj{"binding": *binding, "actor": "agent", "name": *name, "note": *note, "event": *event, "status": *delivery, "revision": *revision, "action": verb, "generation": state["generation"], "file": *file, "line": *line, "function": *fn, "condition": *cond, "hitCondition": *hit, "breakpoint": *bp, "open": !*noOpen}
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
			v, e := api(s, "GET", "/api/state", nil)
			if e != nil {
				return nil, e
			}
			if str(v["status"]) != "running" {
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
