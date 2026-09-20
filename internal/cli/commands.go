package cli

import (
	"flag"
	"fmt"
	"os"
	"time"

	"debug-handover/internal/session"
)

type obj = map[string]any

func usage() string {
	return `Debug Handover — persistent Go / Delve sessions

  debug-handover start --binary PATH --project DIR [--dlv PATH] -- [program args]
  debug-handover sessions
  debug-handover state ID [--goroutine N] [--frame N]
  debug-handover eval ID --expression EXPR [--goroutine N] [--frame N] [--depth 3] [--count 64]
  debug-handover watch|unwatch ID --expression EXPR
  debug-handover bind ID --thread UUID
  debug-handover retry-notification ID
  debug-handover recover ID
  debug-handover cleanup ID
  debug-handover doctor [--binary PATH] [--project DIR]
  debug-handover break ID --file PATH --line N [--condition EXPR] [--hit-condition '== 3']
  debug-handover break ID --function main.process [--condition EXPR]
  debug-handover clear ID --breakpoint N
  debug-handover continue|next|step|stepout ID [--wait 20s]
  debug-handover pause ID
  debug-handover handover ID [--editor zed|vscode] [--no-open]
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
		entries, _ := os.ReadDir(session.Root())
		list := []any{}
		for _, ent := range entries {
			s, e := session.Read(ent.Name())
			if e != nil {
				continue
			}
			if s.Stopped {
				list = append(list, obj{"id": s.ID, "binary": s.Binary, "project": s.Project, "status": "ended"})
				continue
			}
			v, e := api(s, "GET", "/api/state?brief=1", nil)
			if e != nil {
				list = append(list, obj{"id": s.ID, "binary": s.Binary, "project": s.Project, "status": "offline", "recoverable": s.RPC != "" && !s.Stopped, "error": e.Error()})
				continue
			}
			list = append(list, obj{"id": s.ID, "binary": s.Binary, "project": s.Project, "owner": v["owner"], "status": v["status"], "panel": s.HTTP + "/#" + s.Token})
		}
		return list, nil
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
	wait := f.Duration("wait", 0, "wait for pause")
	noOpen := f.Bool("no-open", false, "do not open the editor")
	editor := f.String("editor", "", "handover editor: zed or vscode (defaults to previous editor)")
	expr := f.String("expression", "", "read-only Go expression")
	depth := f.Int("depth", 3, "variable depth (0–6)")
	count := f.Int("count", 64, "maximum array/struct entries (1–128)")
	thread := f.String("thread", "", "Codex task UUID; empty disables wakeups")
	notify := f.Bool("notify", false, "queue a handover message to the bound Codex task")
	if e = f.Parse(args[2:]); e != nil {
		return nil, e
	}
	if len(f.Args()) > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	if verb == "state" {
		return api(s, "GET", fmt.Sprintf("/api/state?goroutine=%d&frame=%d", *gid, *frame), nil)
	}
	state, e := api(s, "GET", "/api/state?brief=1", nil)
	if e != nil {
		return nil, e
	}
	body := obj{"action": verb, "generation": state["generation"], "file": *file, "line": *line, "function": *fn, "condition": *cond, "hitCondition": *hit, "breakpoint": *bp, "open": !*noOpen}
	body["editor"] = *editor
	body["expression"], body["depth"], body["count"], body["goroutine"], body["frame"], body["thread"], body["notify"] = *expr, *depth, *count, *gid, *frame, *thread, *notify
	res, e := api(s, "POST", "/api/action", body)
	if e != nil {
		return nil, e
	}
	if *wait > 0 {
		for deadline := time.Now().Add(*wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			v, e := api(s, "GET", "/api/state", nil)
			if e != nil {
				return nil, e
			}
			if str(v["status"]) != "running" {
				return v, nil
			}
		}
		return nil, fmt.Errorf("wait timed out; session remains alive (use state or pause)")
	}
	return res, nil
}
