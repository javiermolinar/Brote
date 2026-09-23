package cli

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func commentCommand(args []string) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("usage: comment list SESSION | comment reply SESSION THREAD --question ID --body-file PATH --message-id KEY")
	}
	verb, id := args[0], args[1]
	if verb == "index" {
		return session.ReadImportIndex(id)
	}
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
	workspace := f.String("workspace", "", "workspace history index")
	offline := f.Bool("offline", false, "read/write saved history without contacting the target")
	contextMode := f.String("context", "original", "original or current evidence")
	previous := f.String("previous-session", "", "earlier session in this investigation")
	recipientKind := f.String("recipient-kind", "", "agent or provider")
	recipientID := f.String("recipient-id", "", "explicit recipient ID")
	recipientRevision := f.Uint64("recipient-revision", 1, "recipient revision")
	recipientName := f.String("recipient-name", "", "recipient display name")
	body := f.String("body-file", "", "UTF-8 comment text file")
	attempt := f.String("attempt", "", "current delivery attempt")
	question := f.String("question", "", "question message ID")
	key := f.String("message-id", "", "idempotency key for reply")
	binding := f.String("binding", "", "agent binding ID")
	revision := f.Uint64("revision", 0, "binding revision")
	status := f.String("status", "", "delivery state")
	detail := f.String("error", "", "delivery failure")
	file := f.String("file", "", "source file")
	line := f.Int("line", 0, "source line")
	expectedRun := f.String("run", "", "expected execution run for current evidence")
	generation := f.Int("generation", -1, "expected pause generation for current evidence")
	gid := f.Int("goroutine", 0, "goroutine")
	frame := f.Int("frame", 0, "frame")
	expression := f.String("expression", "", "variable expression")
	if err := f.Parse(rest); err != nil {
		return nil, err
	}
	if verb == "import" {
		data, err := os.ReadFile(*body)
		if err != nil {
			return nil, err
		}
		if len(data) > session.MaxDiscussionBytes {
			return nil, fmt.Errorf("import exceeds 32 MiB")
		}
		var in session.NativeImport
		if err = json.Unmarshal(data, &in); err != nil {
			return nil, err
		}
		in.Workspace = id
		return session.ImportNative(in)
	}
	if len(f.Args()) > 0 {
		return nil, fmt.Errorf("unexpected arguments")
	}
	a := obj{"attempt": *attempt, "action": verb, "thread": thread, "question": *question, "messageId": *key, "binding": *binding, "revision": *revision, "status": *status, "error": *detail, "file": *file, "line": *line, "goroutine": *gid, "frame": *frame, "expression": *expression}
	if *contextMode != "original" && *contextMode != "current" {
		return nil, fmt.Errorf("context must be original or current")
	}
	a["contextMode"] = *contextMode
	a["run"] = *expectedRun
	if *previous != "" {
		a["previousRun"] = *previous
	}
	if *recipientKind != "" || *recipientID != "" {
		r := protocol.Recipient{Kind: *recipientKind, ID: *recipientID, Revision: *recipientRevision, Name: *recipientName}
		if err := r.Check(); err != nil {
			return nil, err
		}
		a["recipient"] = r
	}
	if *body != "" {
		data, e := os.ReadFile(*body)
		if e != nil {
			return nil, e
		}
		a["body"] = string(data)
	}
	s, readErr := session.Read(id)
	if *offline || readErr != nil || s.Stopped {
		if verb == "create" || verb == "continue-thread" || (*contextMode == "current" && verb == "ask") {
			return nil, fmt.Errorf("current evidence requires a live session")
		}
		var request session.DiscussionRequest
		data, _ := json.Marshal(a)
		if err := json.Unmarshal(data, &request); err != nil {
			return nil, err
		}
		thread, err := session.MutateDiscussion(id, request)
		if err != nil {
			return nil, err
		}
		return session.DiscussionResult(id, thread, verb), nil
	}
	if verb == "create" || ((verb == "ask" || verb == "continue-thread") && *contextMode == "current") {
		state, e := api(s, "GET", "/api/state?brief=1", nil)
		if e != nil {
			return nil, e
		}
		a["generation"] = state["generation"]
		if *generation >= 0 {
			a["generation"] = *generation
		}
	}
	result, err := api(s, "POST", "/api/comments", a)
	if err == nil && *workspace != "" {
		if t, ok := result["thread"].(map[string]any); ok {
			if threadID, ok := t["id"].(string); ok {
				err = session.LinkDiscussion(*workspace, id, threadID)
			}
		}
	}
	return result, err
}
