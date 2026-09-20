package cli

import (
	"agentdebugger/internal/session"
	"flag"
	"fmt"
	"os"
)

func commentCommand(args []string) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("usage: comment list SESSION | comment reply SESSION THREAD --question ID --body-file PATH --message-id KEY")
	}
	verb, id := args[0], args[1]
	if verb == "list" {
		return session.ReadDiscussion(id)
	}
	f := flag.NewFlagSet("comment "+verb, flag.ContinueOnError)
	thread := ""
	rest := args[2:]
	if len(rest) > 0 && rest[0] != "" && rest[0][0] != '-' {
		thread = rest[0]
		rest = rest[1:]
	}
	body := f.String("body-file", "", "UTF-8 comment text file")
	question := f.String("question", "", "question message ID")
	key := f.String("message-id", "", "idempotency key for reply")
	binding := f.String("binding", "", "agent binding ID")
	revision := f.Uint64("revision", 0, "binding revision")
	status := f.String("status", "", "delivery state")
	detail := f.String("error", "", "delivery failure")
	file := f.String("file", "", "source file")
	line := f.Int("line", 0, "source line")
	gid := f.Int("goroutine", 0, "goroutine")
	frame := f.Int("frame", 0, "frame")
	expression := f.String("expression", "", "variable expression")
	if err := f.Parse(rest); err != nil {
		return nil, err
	}
	if len(f.Args()) > 0 {
		return nil, fmt.Errorf("unexpected arguments")
	}
	s, err := session.Read(id)
	if err != nil {
		return nil, err
	}
	a := obj{"action": verb, "thread": thread, "question": *question, "messageId": *key, "binding": *binding, "revision": *revision, "status": *status, "error": *detail, "file": *file, "line": *line, "goroutine": *gid, "frame": *frame, "expression": *expression}
	if *body != "" {
		data, e := os.ReadFile(*body)
		if e != nil {
			return nil, e
		}
		a["body"] = string(data)
	}
	if verb == "create" {
		state, e := api(s, "GET", "/api/state?brief=1", nil)
		if e != nil {
			return nil, e
		}
		a["generation"] = state["generation"]
	}
	return api(s, "POST", "/api/comments", a)
}
