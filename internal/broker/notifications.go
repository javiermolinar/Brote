package broker

import (
	"agentdebugger/internal/delivery"
	"agentdebugger/internal/session"
	"fmt"
	"strconv"
	"time"
)

// emit is called with mu held. The descriptor is the atomic state/event journal.
func (b *broker) emit(kind, note string) error {
	if len(note) > 4096 {
		note = note[:4096]
	}
	old := b.s
	b.s.Cursor++
	var binding *session.Binding
	if b.s.Binding != nil {
		v := *b.s.Binding
		binding = &v
	}
	event := session.Event{RunID: b.s.RunID, Generation: b.generation, PauseEpoch: b.handleEpoch, ID: b.s.Cursor, Kind: kind, Owner: b.owner, Binding: binding, Note: note, Created: time.Now().UTC().Format(time.RFC3339Nano)}
	b.s.Events = append(append([]session.Event(nil), b.s.Events...), event)
	if len(b.s.Events) > 256 {
		b.s.Events = b.s.Events[len(b.s.Events)-256:]
	}
	if kind == "control_returned" {
		b.s.Notification = &session.Notification{ID: strconv.FormatUint(event.ID, 10), Kind: kind, Status: "pending", Created: event.Created, Note: note, Run: b.s.RunID}
	}
	if err := b.persist(); err != nil {
		b.s = old
		if old.Owner != "" {
			b.owner = old.Owner
		}
		return err
	}
	switch kind {
	case "stopped":
		b.historyStop(note)
	case "target_exited", "terminated":
		if b.activeTask() {
			b.s.Task.Status = "completed"
			b.record("task.completed", "debugger", b.s.Task)
			_ = b.persist()
		}
		b.record("session.ended", "debugger", obj{"reason": kind})
	default:
		b.record("session."+kind, "core", event)
	}
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
	return nil
}
func (b *broker) queueNotification(kind string) error {
	return b.emit("control_returned", "Explicit notification retry")
}

func (b *broker) eventStatus(a obj) (obj, error) {
	n := b.s.Notification
	if n == nil || n.ID != str(a["event"]) || b.owner != "agent" || b.s.Binding == nil || b.s.Binding.ID != str(a["binding"]) || uint64(num(a["revision"])) != b.s.Binding.Revision {
		return nil, fmt.Errorf("event or binding is obsolete")
	}
	status := str(a["status"])
	if n.Attempt != nil && str(a["attempt"]) != n.Attempt.ID {
		return nil, fmt.Errorf("delivery attempt required")
	}
	next, err := delivery.Transition(n.Status, status)
	// Legacy recipients may acknowledge a visible pending handback directly.
	if err != nil && status == "acknowledged" && n.Status == "pending" && n.Attempt == nil {
		next, err = status, nil
	}
	if err != nil {
		return nil, err
	}
	if next != status {
		return obj{"notification": n}, nil
	}
	copy := *n
	copy.Status = next
	copy.Error = str(a["error"])
	if len(copy.Error) > 1024 {
		copy.Error = copy.Error[:1024]
	}
	b.s.Notification = &copy
	if err := b.persist(); err != nil {
		b.s.Notification = n
		return nil, err
	}
	return obj{"notification": copy}, nil
}
