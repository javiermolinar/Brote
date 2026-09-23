package broker

import (
	"agentdebugger/internal/delivery"
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
)

func agentRecipient(binding *session.Binding) protocol.Recipient {
	if binding == nil {
		return protocol.Recipient{}
	}
	return protocol.Recipient{Kind: "agent", ID: binding.ID, Revision: binding.Revision, Name: binding.Name}
}
func sameRecipient(a, b protocol.Recipient) bool {
	return a.Kind == b.Kind && a.ID == b.ID && a.Revision == b.Revision
}
func questionRecipient(d session.CommentDelivery) protocol.Recipient {
	if d.Recipient != nil {
		return *d.Recipient
	}
	return agentRecipient(d.Binding)
}

type deliverySubject struct {
	kind, id, thread, run, body, status string
	recipient                           protocol.Recipient
	attempt                             *delivery.Attempt
	evidence                            *session.EvidenceIdentity
	save                                func(string, *delivery.Attempt, string) error
}

// Called under the broker mutex. Each subject's attempt and domain state share
// an atomic document. Cursor writes may lag but cannot expose an unclaimed send.
func (b *broker) deliverySubjects() ([]deliverySubject, error) {
	subjects := []deliverySubject{}
	if n := b.s.Notification; n != nil && b.owner == "agent" && b.s.Binding != nil {
		note := n.Note
		if note == "" {
			for _, e := range b.s.Events {
				if strconv.FormatUint(e.ID, 10) == n.ID {
					note = e.Note
				}
			}
		}
		run := n.Run
		if run == "" {
			run = b.s.RunID
		}
		if run == b.s.RunID {
			subjects = append(subjects, deliverySubject{kind: "handback", id: n.ID, run: run, body: note, status: n.Status, recipient: agentRecipient(b.s.Binding), attempt: n.Attempt, save: func(status string, a *delivery.Attempt, detail string) error {
				copy := *n
				copy.Note = note
				copy.Run = run
				copy.Status = status
				copy.Attempt = a
				copy.Error = detail
				b.s.Notification = &copy
				if err := b.persist(); err != nil {
					b.s.Notification = n
					return err
				}
				return nil
			}})
		}
	}
	if t := b.s.Task; b.activeTask() && t.Binding != nil && b.s.Binding != nil && *t.Binding == *b.s.Binding {
		run := t.Run
		if run == "" {
			run = b.s.RunID
		}
		if run == b.s.RunID {
			subjects = append(subjects, deliverySubject{kind: "task", id: t.ID, run: run, body: t.Instruction, status: t.Delivery, recipient: agentRecipient(t.Binding), attempt: t.Attempt, save: func(status string, a *delivery.Attempt, detail string) error {
				copy := *t
				copy.Run = run
				copy.Delivery = status
				copy.Attempt = a
				copy.DeliveryError = detail
				b.s.Task = &copy
				if err := b.persist(); err != nil {
					b.s.Task = t
					return err
				}
				return nil
			}})
		}
	}
	d, err := session.ReadDiscussion(b.s.ID)
	if err != nil {
		return nil, err
	}
	for i := range d.Threads {
		t := &d.Threads[i]
		if t.Resolved || t.Delivery.Question == "" {
			continue
		}
		r := questionRecipient(t.Delivery)
		if r.Kind == "agent" && !sameRecipient(r, agentRecipient(b.s.Binding)) {
			continue
		}
		body := ""
		var evidence *session.EvidenceIdentity
		for _, m := range t.Messages {
			if m.ID == t.Delivery.Question {
				body = m.Body
				evidence = m.Evidence
			}
		}
		subjects = append(subjects, deliverySubject{kind: "question", id: t.Delivery.Question, thread: t.ID, body: body, evidence: evidence, status: t.Delivery.Status, recipient: r, attempt: t.Delivery.Attempt, save: func(status string, a *delivery.Attempt, detail string) error {
			previous := t.Delivery
			t.Delivery.Status = status
			t.Delivery.Attempt = a
			if a != nil {
				t.Delivery.AttemptRequired = true
			}
			t.Delivery.Error = detail
			if err := session.CommitDiscussion(&d); err != nil {
				t.Delivery = previous
				return err
			}
			b.historyDiscussion(d, "delivery")
			return nil
		}})
	}
	return subjects, nil
}

func (b *broker) deliveryAction(a obj) (obj, error) {
	if b.s.ServiceVersion == 0 {
		return nil, &protocol.Error{Code: "unsupported_operation", Message: "managed delivery requires a shared-service session"}
	}
	verb, id := str(a["action"]), str(a["consumer"])
	if id == "" || len(id) > 256 {
		return nil, fmt.Errorf("consumer ID required (maximum 256 bytes)")
	}
	if verb == "consumer-open" {
		var r protocol.Recipient
		data, _ := json.Marshal(a["recipient"])
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		if err := r.Check(); err != nil {
			return nil, err
		}
		if r.Kind == "agent" && !sameRecipient(r, agentRecipient(b.s.Binding)) {
			return nil, fmt.Errorf("agent binding changed")
		}
		if _, exists := b.s.Consumers[id]; !exists && len(b.s.Consumers) >= 32 {
			return nil, fmt.Errorf("consumer limit reached")
		}
		subjects, err := b.deliverySubjects()
		if err != nil {
			return nil, err
		}
		for _, s := range subjects {
			if s.attempt != nil && s.attempt.Consumer == id && s.status == "sending" {
				if err := s.save("unknown", s.attempt, "host replaced during send; check conversation before retrying"); err != nil {
					return nil, err
				}
			}
		}
		old := b.s.Consumers
		b.s.Consumers = maps.Clone(old)
		if b.s.Consumers == nil {
			b.s.Consumers = map[string]session.Consumer{}
		}
		c := session.Consumer{ID: id, Instance: session.NewID(16), Recipient: r, Cursor: b.s.Cursor}
		b.s.Consumers[id] = c
		if err := b.persist(); err != nil {
			b.s.Consumers = old
			return nil, err
		}
		return obj{"consumer": c}, nil
	}
	c, ok := b.s.Consumers[id]
	if !ok || c.Instance != str(a["instance"]) {
		return nil, fmt.Errorf("consumer instance changed")
	}
	if c.Recipient.Kind == "agent" && !sameRecipient(c.Recipient, agentRecipient(b.s.Binding)) {
		return nil, fmt.Errorf("agent binding changed")
	}
	if verb == "consumer-challenge" || verb == "consumer-fact" || verb == "consumer-close" {
		return b.hostAction(c, a)
	}
	subjects, err := b.deliverySubjects()
	if err != nil {
		return nil, err
	}
	switch verb {
	case "consumer-next":
		for _, s := range subjects {
			if !sameRecipient(s.recipient, c.Recipient) || s.status != "pending" {
				continue
			}
			if s.kind == "task" {
				if err := b.taskValid(obj{"task": s.id, "binding": c.Recipient.ID}); err != nil {
					continue
				}
			}
			attempt := &delivery.Attempt{ID: session.NewID(16), Consumer: id, Instance: c.Instance, Recipient: c.Recipient, Run: s.run}
			if err := s.save("sending", attempt, ""); err != nil {
				return nil, err
			}
			e := protocol.DeliveryEnvelope{Version: 1, Session: b.s.ID, Run: s.run, Kind: s.kind, Subject: s.id, Thread: s.thread, Attempt: attempt.ID, Recipient: c.Recipient, Cursor: b.s.Cursor, Status: "sending"}
			if s.evidence != nil {
				e.EvidenceID = s.evidence.ID
				e.EvidenceRun = s.evidence.ExecutionRun
			}
			e.Message = deliveryMessage(e, s.body)
			if err := e.Check(); err != nil {
				return nil, err
			}
			return obj{"delivery": e, "cursor": c.Cursor}, nil
		}
		if c.Cursor == b.s.Cursor {
			return obj{"cursor": c.Cursor}, nil
		}
		old := c
		c.Cursor = b.s.Cursor
		b.s.Consumers[id] = c
		if err := b.persist(); err != nil {
			b.s.Consumers[id] = old
			return nil, err
		}
		return obj{"cursor": c.Cursor}, nil
	case "event-status":
		for _, s := range subjects {
			if s.kind != str(a["kind"]) || s.id != str(a["subject"]) || s.thread != str(a["thread"]) {
				continue
			}
			if err := s.attempt.Check(str(a["attempt"]), id, c.Instance, c.Recipient); err != nil {
				return nil, err
			}
			requested := str(a["status"])
			if requested == "answered" || requested == "sending" || (requested == "thinking" && s.kind != "question") {
				return nil, fmt.Errorf("status requires its domain operation")
			}
			next, err := delivery.Transition(s.status, requested)
			if err != nil {
				return nil, err
			}
			detail := str(a["error"])
			if len(detail) > 1024 {
				detail = detail[:1024]
			}
			if next != str(a["status"]) {
				detail = ""
			}
			if err := s.save(next, s.attempt, detail); err != nil {
				return nil, err
			}
			return obj{"status": next}, nil
		}
		return nil, fmt.Errorf("delivery subject is obsolete")
	default:
		return nil, fmt.Errorf("unknown consumer action")
	}
}

func deliveryMessage(e protocol.DeliveryEnvelope, body string) string {
	return protocol.DeliveryMessage(e, body)
}
