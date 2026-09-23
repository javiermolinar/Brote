package session

import (
	"agentdebugger/internal/delivery"
	"agentdebugger/internal/protocol"
	"fmt"
	"strings"
	"time"
)

// DiscussionRequest describes storage operations; none can acquire execution.
type DiscussionRequest struct {
	Action    string              `json:"action"`
	Thread    string              `json:"thread"`
	Question  string              `json:"question"`
	Body      string              `json:"body"`
	MessageID string              `json:"messageId"`
	Binding   string              `json:"binding"`
	Revision  uint64              `json:"revision"`
	Attempt   string              `json:"attempt"`
	Status    string              `json:"status"`
	Error     string              `json:"error"`
	Recipient *protocol.Recipient `json:"recipient,omitempty"`
}

func DiscussionRecipient(d CommentDelivery) protocol.Recipient {
	if d.Recipient != nil {
		return *d.Recipient
	}
	if d.Binding != nil {
		return protocol.Recipient{Kind: "agent", ID: d.Binding.ID, Revision: d.Binding.Revision, Name: d.Binding.Name}
	}
	return protocol.Recipient{}
}
func ApplyDiscussion(d *Discussion, a DiscussionRequest) (*CommentThread, error) {
	var t *CommentThread
	for i := range d.Threads {
		if d.Threads[i].ID == a.Thread {
			t = &d.Threads[i]
			break
		}
	}
	if t == nil {
		return nil, fmt.Errorf("comment thread not found")
	}
	body := strings.TrimSpace(a.Body)
	switch a.Action {
	case "ask":
		if body == "" || len(body) > 16000 {
			return nil, fmt.Errorf("question must contain 1–16000 bytes")
		}
		if !t.Resolved && t.Delivery.Status != "answered" {
			return nil, fmt.Errorf("wait for the current answer or resolve the thread first")
		}
		if len(t.Messages) >= 200 {
			return nil, fmt.Errorf("thread message limit reached")
		}
		r := DiscussionRecipient(t.Delivery)
		if a.Recipient != nil {
			r = *a.Recipient
		}
		if err := r.Check(); err != nil {
			return nil, err
		}
		context := t.Context
		var evidence *EvidenceIdentity
		for _, m := range t.Messages {
			if m.Author == "human" && m.Context != nil {
				context = m.Context
				evidence = m.Evidence
				break
			}
		}
		id := NewID(8)
		t.Messages = append(t.Messages, CommentMessage{ID: id, Author: "human", Body: body, Created: time.Now().UTC().Format(time.RFC3339Nano), Run: d.Session, Context: context, Evidence: evidence})
		t.Context = context
		t.Resolved = false
		t.Delivery = CommentDelivery{Question: id, Status: "pending", Recipient: &r}
	case "claim":
		r := DiscussionRecipient(t.Delivery)
		if a.Recipient == nil || *a.Recipient != r || t.Resolved || t.Delivery.Question != a.Question || t.Delivery.Status != "pending" {
			return nil, fmt.Errorf("question or recipient is obsolete or already claimed")
		}
		t.Delivery.Attempt = &delivery.Attempt{ID: NewID(16), Consumer: r.Kind + ":" + r.ID, Instance: NewID(16), Recipient: r}
		t.Delivery.AttemptRequired = true
		t.Delivery.Status = "sending"
	case "reply", "delivery", "answer-failed":
		r := DiscussionRecipient(t.Delivery)
		supplied := protocol.Recipient{Kind: "agent", ID: a.Binding, Revision: a.Revision}
		if a.Recipient != nil {
			supplied = *a.Recipient
		}
		if t.Resolved || t.Delivery.Question != a.Question || r.Kind != supplied.Kind || r.ID != supplied.ID || r.Revision != supplied.Revision || r.ID == "" {
			return nil, fmt.Errorf("question or recipient is obsolete")
		}
		if (t.Delivery.Attempt == nil && (t.Delivery.AttemptRequired || a.Attempt != "")) || (t.Delivery.Attempt != nil && a.Attempt != t.Delivery.Attempt.ID) {
			return nil, fmt.Errorf("delivery attempt required")
		}
		if a.Action == "answer-failed" {
			if t.Delivery.Status != "answered" {
				t.Delivery.Status = "failed"
				t.Delivery.Error = a.Error
				if len(t.Delivery.Error) > 1024 {
					t.Delivery.Error = t.Delivery.Error[:1024]
				}
			}
			return t, nil
		}
		if a.Action == "reply" {
			if body == "" || len(body) > MaxAnswerBytes {
				return nil, fmt.Errorf("answer must contain 1–131072 bytes")
			}
			if a.MessageID == "" || len(a.MessageID) > 128 {
				return nil, fmt.Errorf("reply messageId required (maximum 128 bytes)")
			}
			for _, m := range t.Messages {
				if m.ID == a.MessageID {
					if m.Body == body && m.Question == a.Question {
						return t, nil
					}
					return nil, fmt.Errorf("reply key already used")
				}
			}
			if t.Delivery.Status == "answered" {
				return nil, fmt.Errorf("question already answered")
			}
			if len(t.Messages) >= 200 {
				return nil, fmt.Errorf("thread message limit reached")
			}
			t.Messages = append(t.Messages, CommentMessage{ID: a.MessageID, Author: r.Name, Body: body, Created: time.Now().UTC().Format(time.RFC3339Nano), Question: a.Question, Recipient: &r})
			t.Delivery.Status = "answered"
			t.Delivery.Error = ""
		} else {
			next, err := delivery.Transition(t.Delivery.Status, a.Status)
			if err != nil {
				return nil, err
			}
			if a.Status == "answered" {
				return nil, fmt.Errorf("answer requires reply operation")
			}
			if next == a.Status {
				t.Delivery.Status = next
				t.Delivery.Error = a.Error
				if len(t.Delivery.Error) > 1024 {
					t.Delivery.Error = t.Delivery.Error[:1024]
				}
			}
		}
	case "resolve":
		t.Resolved = true
	case "reopen":
		t.Resolved = false
	case "retry":
		if t.Resolved || (t.Delivery.Status != "unknown" && t.Delivery.Status != "failed" && t.Delivery.Status != "pending" && !(DiscussionRecipient(t.Delivery).Kind == "provider" && t.Delivery.Status != "answered")) {
			return nil, fmt.Errorf("only undelivered questions can be retried")
		}
		if a.Recipient != nil {
			if err := a.Recipient.Check(); err != nil {
				return nil, err
			}
			t.Delivery.Recipient = a.Recipient
			t.Delivery.Binding = nil
		}
		t.Delivery.AttemptRequired = t.Delivery.AttemptRequired || t.Delivery.Attempt != nil
		t.Delivery.Attempt = nil
		t.Delivery.Status = "pending"
		t.Delivery.Error = ""
	default:
		return nil, fmt.Errorf("operation requires live evidence capture")
	}
	return t, nil
}

// MutateDiscussion is a storage-only writer, including after runtime cache
// removal. CAS retry reruns current-question validation on the newest document.
func MutateDiscussion(id string, a DiscussionRequest) (*CommentThread, error) {
	for retries := 0; retries < 8; retries++ {
		d, err := ReadDiscussion(id)
		if err != nil {
			return nil, err
		}
		t, err := ApplyDiscussion(&d, a)
		if err != nil {
			return nil, err
		}
		if err = CommitDiscussion(&d); err == ErrDiscussionConflict {
			continue
		} else if err != nil {
			return nil, err
		}
		return t, nil
	}
	return nil, ErrDiscussionConflict
}

func DiscussionResult(id string, t *CommentThread, action string) map[string]any {
	result := map[string]any{"thread": t}
	if action != "claim" || t.Delivery.Attempt == nil {
		return result
	}
	e := protocol.DeliveryEnvelope{Version: protocol.CoordinationVersion, Session: id, Kind: "question", Subject: t.Delivery.Question, Thread: t.ID, Attempt: t.Delivery.Attempt.ID, Recipient: DiscussionRecipient(t.Delivery), Status: t.Delivery.Status}
	body := ""
	for _, m := range t.Messages {
		if m.ID == e.Subject {
			body = m.Body
			if m.Evidence != nil {
				e.EvidenceID = m.Evidence.ID
				e.EvidenceRun = m.Evidence.ExecutionRun
			}
		}
	}
	e.Message = protocol.DeliveryMessage(e, body)
	result["delivery"] = e
	return result
}
