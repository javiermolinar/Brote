package broker

import (
	"agentdebugger/internal/session"
	"path/filepath"
	"testing"
)

func TestCommentsPersistAndNeverTransferControl(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", home)
	binding := &session.Binding{ID: "pi:test", Name: "Pi", Revision: 1}
	b := &broker{s: session.Descriptor{ID: "0123456789", Dir: t.TempDir(), Binding: binding}, owner: "browser", generation: 9}
	d := session.Discussion{Session: b.s.ID, Threads: []session.CommentThread{{ID: "thread", Context: map[string]any{"generation": 3}, Messages: []session.CommentMessage{{ID: "question", Author: "human", Body: "Why?"}}, Delivery: session.CommentDelivery{Question: "question", Status: "pending", Binding: binding}}}}
	if err := session.WriteDiscussion(d); err != nil {
		t.Fatal(err)
	}
	command := obj{"action": "delivery", "thread": "thread", "question": "question", "binding": "pi:test", "revision": 1, "status": "sending"}
	if _, err := b.comments(command); err != nil {
		t.Fatal(err)
	}
	if _, err := b.comments(command); err == nil {
		t.Fatal("duplicate claim accepted")
	}
	command["status"] = "thinking"
	if _, err := b.comments(command); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"thinking", "queued", "unknown"} {
		command["status"] = status
		if _, err := b.comments(command); err != nil {
			t.Fatal(err)
		}
		current, _ := session.ReadDiscussion(b.s.ID)
		if current.Threads[0].Delivery.Status != "thinking" {
			t.Fatal("late receipt overwrote acknowledgement")
		}
	}
	command["action"] = "reply"
	command["body"] = "Captured value was 21"
	command["messageId"] = "answer"
	if _, err := b.comments(command); err != nil {
		t.Fatal(err)
	}
	if _, err := b.comments(command); err != nil {
		t.Fatal("idempotent retry:", err)
	}
	persisted, err := session.ReadDiscussion(b.s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Threads[0].Messages) != 2 || persisted.Threads[0].Delivery.Status != "answered" {
		t.Fatal(persisted)
	}
	if b.owner != "browser" || b.generation != 9 || b.s.Notification != nil {
		t.Fatal("discussion modified execution or handback state")
	}
	if b.s.Events[len(b.s.Events)-1].Kind != "reply.added" {
		t.Fatal(b.s.Events)
	}
	// A new broker sees the same record, even when its runtime descriptor is elsewhere.
	other := &broker{s: session.Descriptor{ID: b.s.ID, Dir: filepath.Join(home, "missing")}, owner: "agent"}
	view, err := other.comments(nil)
	if err != nil || len(view["discussion"].(session.Discussion).Threads) != 1 {
		t.Fatal(view, err)
	}
	command["revision"] = 2
	if _, err = b.comments(command); err == nil {
		t.Fatal("obsolete binding accepted")
	}
	command["revision"] = 1
	command["question"] = "old"
	if _, err = b.comments(command); err == nil {
		t.Fatal("stale question accepted")
	}
	if _, err = b.comments(obj{"action": "resolve", "thread": "thread"}); err != nil {
		t.Fatal(err)
	}
	command["question"] = "question"
	if _, err = b.comments(command); err == nil {
		t.Fatal("reply to resolved question accepted")
	}
}
func TestCommentWriteFailureDoesNotEmitEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", home)
	b := &broker{s: session.Descriptor{ID: "0123456789", Dir: t.TempDir()}}
	if _, err := b.comments(obj{"action": "create", "generation": 1, "body": "question"}); err == nil {
		t.Fatal("stale pause accepted")
	}
	if b.s.Cursor != 0 {
		t.Fatal("failed question emitted event")
	}
	for _, id := range []string{"../bad", "", "0123456789/"} {
		if _, err := session.ReadDiscussion(id); err == nil {
			t.Fatal(id)
		}
	}
}
