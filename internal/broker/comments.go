package broker

import (
	"agentdebugger/internal/session"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Comment writes are serialized by the broker, but their durable document lives
// outside the runtime cache. No comment operation transfers or uses execution control.
func (b *broker) comments(a obj) (obj, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, err := session.ReadDiscussion(b.s.ID)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return obj{"discussion": d}, nil
	}
	action := str(a["action"])
	body := strings.TrimSpace(str(a["body"]))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if action == "continue-thread" {
		previous := str(a["previousRun"])
		aGroup, e := session.InvestigationFor(previous)
		if e != nil {
			return nil, e
		}
		bGroup, e := session.InvestigationFor(b.s.ID)
		if e != nil {
			return nil, e
		}
		if previous == b.s.ID || aGroup != bGroup {
			return nil, fmt.Errorf("thread must belong to an earlier run of this investigation")
		}
		old, e := session.ReadDiscussion(previous)
		if e != nil {
			return nil, e
		}
		found := false
		for _, thread := range old.Threads {
			if thread.ID == str(a["thread"]) {
				exists := false
				for _, current := range d.Threads {
					if current.ID == thread.ID {
						exists = true
					}
				}
				if !exists {
					if len(d.Threads) >= 200 {
						return nil, fmt.Errorf("thread limit reached")
					}
					for i := range thread.Messages {
						if thread.Messages[i].Context == nil && thread.Messages[i].Author == "human" {
							thread.Messages[i].Context = thread.Context
							thread.Messages[i].Run = previous
						}
					}
					thread.Resolved = true
					d.Threads = append(d.Threads, thread)
				}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("previous thread not found")
		}
		action = "ask"
	}
	if action == "create" || action == "ask" || action == "reply" {
		if body == "" || len(body) > 16000 {
			return nil, fmt.Errorf("comment must contain 1–16000 bytes")
		}
	}
	var t *session.CommentThread
	for i := range d.Threads {
		if d.Threads[i].ID == str(a["thread"]) {
			t = &d.Threads[i]
			break
		}
	}
	kind := "thread.updated"
	if action == "create" {
		if len(d.Threads) >= 200 {
			return nil, fmt.Errorf("session has reached 200 comment threads")
		}
		if num(a["generation"]) != b.generation {
			return nil, fmt.Errorf("pause changed; refresh before asking")
		}
		context, e := b.snapshotLocked(num(a["goroutine"]), num(a["frame"]), false)
		if e != nil {
			return nil, e
		}
		if context["status"] != "paused" || truth(asObj(context["state"])["NextInProgress"]) {
			return nil, fmt.Errorf("pause must be settled before capturing a question")
		}
		file, line := str(a["file"]), num(a["line"])
		files, e := b.rpc("ListSources", obj{"Filter": ""})
		if e != nil {
			return nil, e
		}
		allowed := false
		for _, f := range asList(files["Sources"]) {
			if f == file {
				allowed = true
				break
			}
		}
		if !allowed || line < 1 {
			return nil, fmt.Errorf("select a source line from this binary")
		}
		data, e := readSource(file)
		if e != nil {
			return nil, e
		}
		lines := strings.Split(string(data), "\n")
		if line > len(lines) {
			return nil, fmt.Errorf("line outside source")
		}
		start, end := max(1, line-4), min(len(lines), line+4)
		context = pick(context, "generation", "goroutine", "frame", "frames", "source", "sourceIdentity", "state", "breakpoints", "watches")
		context["anchorSource"] = obj{"file": file, "line": line, "start": start, "lines": lines[start-1 : end]}
		context["capturedAt"] = now
		context["binaryIdentity"] = b.s.Fingerprint
		expression := str(a["expression"])
		if expression != "" {
			v, e := b.evaluate(expression, num(context["goroutine"]), num(context["frame"]), 2, 32, asObj(context["state"]))
			if e != nil {
				return nil, e
			}
			context["expression"] = v
		}
		encoded, _ := json.Marshal(context)
		if len(encoded) > 1024*1024 {
			return nil, fmt.Errorf("captured context exceeds 1 MiB; select a smaller frame")
		}
		d.Threads = append(d.Threads, session.CommentThread{ID: session.NewID(8), File: file, Line: line, Expression: expression, Created: now, Context: context, Messages: []session.CommentMessage{}})
		t = &d.Threads[len(d.Threads)-1]
		d.Binary = b.s.Binary
		d.Project = b.s.Project
	} else if t == nil {
		return nil, fmt.Errorf("comment thread not found")
	}
	switch action {
	case "create", "ask":
		if action == "ask" && t.Delivery.Status != "answered" && !t.Resolved {
			return nil, fmt.Errorf("wait for the current answer or resolve the thread first")
		}
		if len(t.Messages) >= 200 {
			return nil, fmt.Errorf("thread message limit reached")
		}
		context := t.Context
		if action == "ask" && str(a["contextMode"]) == "current" {
			if num(a["generation"]) != b.generation {
				return nil, fmt.Errorf("pause changed; refresh before replying")
			}
			fresh, e := b.snapshotLocked(num(a["goroutine"]), num(a["frame"]), false)
			if e != nil {
				return nil, e
			}
			if fresh["status"] != "paused" {
				return nil, fmt.Errorf("pause the program before capturing current context")
			}
			context = pick(fresh, "generation", "goroutine", "frame", "frames", "source", "sourceIdentity", "state", "breakpoints", "watches")
			context["capturedAt"] = now
			encoded, _ := json.Marshal(context)
			if len(encoded) > 1024*1024 {
				return nil, fmt.Errorf("captured context exceeds 1 MiB")
			}
		} else if action == "ask" && len(t.Messages) > 0 && t.Messages[0].Context != nil {
			context = t.Messages[0].Context
		}
		// Older documents stored only thread-level context. Preserve it before
		// replacing the latest-question view consumed by existing agent adapters.
		for i := range t.Messages {
			if t.Messages[i].Author == "human" && t.Messages[i].Context == nil {
				t.Messages[i].Context = t.Context
				t.Messages[i].Run = b.s.ID
			}
		}
		t.Context = context
		id := session.NewID(8)
		t.Messages = append(t.Messages, session.CommentMessage{ID: id, Author: "human", Body: body, Created: now, Context: context, Run: b.s.ID})
		t.Resolved = false
		t.Delivery = session.CommentDelivery{Question: id, Status: "pending", Binding: copyBinding(b.s.Binding)}
		kind = "question.created"
	case "reply", "delivery":
		if t.Resolved || t.Delivery.Question != str(a["question"]) || !matchesCommentBinding(b.s.Binding, t.Delivery.Binding, a) {
			return nil, fmt.Errorf("question or agent binding is obsolete")
		}
		if action == "reply" {
			// A retry with the same reply key must never duplicate a posted answer.
			key := str(a["messageId"])
			if key == "" || len(key) > 128 {
				return nil, fmt.Errorf("reply messageId required (maximum 128 bytes)")
			}
			for _, m := range t.Messages {
				if m.ID == key {
					if m.Body == body && m.Question == t.Delivery.Question {
						return obj{"thread": t}, nil
					}
					return nil, fmt.Errorf("reply key already used")
				}
			}
			if t.Delivery.Status == "answered" {
				return nil, fmt.Errorf("question already answered")
			}
			t.Messages = append(t.Messages, session.CommentMessage{ID: key, Author: b.s.Binding.Name, Body: body, Created: now, Question: t.Delivery.Question})
			t.Delivery.Status = "answered"
			t.Delivery.Error = ""
			kind = "reply.added"
		} else {
			status := str(a["status"])
			previous := t.Delivery.Status
			// Agent acknowledgements can beat the listener's delivery receipt.
			if (previous == "thinking" || previous == "answered") && (status == "queued" || status == "failed" || status == "unknown" || status == "thinking") {
				return obj{"thread": t}, nil
			}
			if !((status == "thinking" && (previous == "sending" || previous == "queued" || previous == "unknown")) || (status == "sending" && previous == "pending") || ((status == "queued" || status == "failed" || status == "unknown") && previous == "sending")) {
				return nil, fmt.Errorf("invalid delivery transition %s -> %s", previous, status)
			}
			t.Delivery.Status = status
			t.Delivery.Error = str(a["error"])
			if len(t.Delivery.Error) > 1024 {
				t.Delivery.Error = t.Delivery.Error[:1024]
			}
		}
	case "resolve":
		t.Resolved = true
		kind = "thread.resolved"
	case "reopen":
		t.Resolved = false
	case "retry":
		if t.Resolved || (t.Delivery.Status != "unknown" && t.Delivery.Status != "failed" && t.Delivery.Status != "pending") {
			return nil, fmt.Errorf("only undelivered questions can be retried")
		}
		t.Delivery.Status = "pending"
		t.Delivery.Error = ""
		t.Delivery.Binding = copyBinding(b.s.Binding)
		kind = "question.created"
	default:
		return nil, fmt.Errorf("unknown comment action")
	}
	if encoded, e := json.Marshal(d); e != nil {
		return nil, e
	} else if len(encoded) > 16*1024*1024 {
		return nil, fmt.Errorf("discussion exceeds 16 MiB; start another investigation")
	}
	if err = session.WriteDiscussion(d); err != nil {
		return nil, err
	}
	b.historyDiscussion(d, action)
	result := obj{"thread": t}
	// The document is authoritative. Reconnecting adapters reconcile pending
	// questions even if the bounded event journal expired or this event fails.
	if err = b.emit(kind, t.ID); err != nil {
		result["eventError"] = err.Error()
	}
	return result, nil
}
func copyBinding(b *session.Binding) *session.Binding {
	if b == nil {
		return nil
	}
	v := *b
	return &v
}
func matchesCommentBinding(current, captured *session.Binding, a obj) bool {
	return current != nil && captured != nil && *current == *captured && current.ID == str(a["binding"]) && current.Revision == uint64(num(a["revision"]))
}
