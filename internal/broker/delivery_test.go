package broker

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func deliveryFixture(t *testing.T) *broker {
	t.Helper()
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	return &broker{s: session.Descriptor{ID: "0123456789", Dir: t.TempDir(), ServiceVersion: 1, RunID: "run", Binding: &session.Binding{ID: "pi", Revision: 1, Name: "Pi"}}, owner: "agent", generation: 1}
}
func openConsumer(t *testing.T, b *broker, id string) session.Consumer {
	t.Helper()
	r, err := b.deliveryAction(obj{"action": "consumer-open", "consumer": id, "recipient": agentRecipient(b.s.Binding)})
	if err != nil {
		t.Fatal(err)
	}
	return r["consumer"].(session.Consumer)
}
func nextDelivery(t *testing.T, b *broker, c session.Consumer) *protocol.DeliveryEnvelope {
	t.Helper()
	r, err := b.deliveryAction(obj{"action": "consumer-next", "consumer": c.ID, "instance": c.Instance})
	if err != nil {
		t.Fatal(err)
	}
	if r["delivery"] == nil {
		return nil
	}
	e := r["delivery"].(protocol.DeliveryEnvelope)
	return &e
}
func receipt(b *broker, c session.Consumer, e *protocol.DeliveryEnvelope, status string) error {
	_, err := b.deliveryAction(obj{"action": "event-status", "consumer": c.ID, "instance": c.Instance, "kind": e.Kind, "subject": e.Subject, "thread": e.Thread, "attempt": e.Attempt, "status": status})
	return err
}
func TestManagedDeliveryClaimAckAndCursorRecovery(t *testing.T) {
	b := deliveryFixture(t)
	if err := b.emit("control_returned", "durable note"); err != nil {
		t.Fatal(err)
	}
	c := openConsumer(t, b, "host")
	e := nextDelivery(t, b, c)
	if e == nil {
		t.Fatal("no handback")
	}
	// A competing consumer cannot inject the same subject.
	other := openConsumer(t, b, "other")
	if nextDelivery(t, b, other) != nil {
		t.Fatal("duplicate claim")
	}
	if err := receipt(b, other, e, "queued"); err == nil {
		t.Fatal("wrong claimant receipt")
	}
	if err := receipt(b, c, e, "acknowledged"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"queued", "unknown", "failed", "acknowledged"} {
		if err := receipt(b, c, e, s); err != nil {
			t.Fatal(err)
		}
	}
	if b.s.Notification.Status != "acknowledged" {
		t.Fatal(b.s.Notification)
	}
	// Persisted subject survives a cursor that has not advanced.
	var restored session.Descriptor
	data, err := os.ReadFile(filepath.Join(b.s.Dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	b.s = restored
	if nextDelivery(t, b, c) != nil {
		t.Fatal("replayed acknowledged subject")
	}
}
func TestManagedPendingHandbackSurvivesJournalExpiry(t *testing.T) {
	b := deliveryFixture(t)
	if err := b.emit("control_returned", "retained handback note"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 260; i++ {
		if err := b.emit("inspection", "other"); err != nil {
			t.Fatal(err)
		}
	}
	c := openConsumer(t, b, "host")
	e := nextDelivery(t, b, c)
	if e == nil || e.Subject != "1" || b.s.Notification.Note != "retained handback note" {
		t.Fatal(e)
	}
	replacement := openConsumer(t, b, "host")
	if b.s.Notification.Status != "unknown" || nextDelivery(t, b, replacement) != nil {
		t.Fatal("uncertain send reinjected")
	}
	if err := receipt(b, c, e, "queued"); err == nil {
		t.Fatal("old instance accepted")
	}
	if err := b.queueNotification("reclaim"); err != nil {
		t.Fatal(err)
	}
	fresh := nextDelivery(t, b, replacement)
	if fresh == nil || fresh.Attempt == e.Attempt {
		t.Fatal("retry did not create new attempt")
	}
}
func TestManagedQuestionClaimPersistsBeforeCursorAndRejectsStaleAttempt(t *testing.T) {
	b := deliveryFixture(t)
	d := session.Discussion{Session: b.s.ID, Threads: []session.CommentThread{{ID: "thread", Messages: []session.CommentMessage{{ID: "q", Body: "Why?", Author: "human"}}, Delivery: session.CommentDelivery{Question: "q", Status: "pending", Binding: b.s.Binding}}}}
	if err := session.WriteDiscussion(d); err != nil {
		t.Fatal(err)
	}
	c := openConsumer(t, b, "host")
	e := nextDelivery(t, b, c)
	if e == nil || e.Kind != "question" {
		t.Fatal(e)
	}
	if err := receipt(b, c, e, "thinking"); err != nil {
		t.Fatal(err)
	}
	if err := receipt(b, c, e, "queued"); err != nil {
		t.Fatal(err)
	}
	a := obj{"action": "reply", "thread": "thread", "question": "q", "binding": "pi", "revision": 1, "messageId": "answer", "body": "Saved evidence", "attempt": "old"}
	if _, err := b.comments(a); err == nil {
		t.Fatal("stale attempt replied")
	}
	a["attempt"] = e.Attempt
	for i := 0; i < 2; i++ {
		if _, err := b.comments(a); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := session.ReadDiscussion(b.s.ID)
	if err != nil || len(saved.Threads[0].Messages) != 2 {
		t.Fatal(saved, err)
	}
	if saved.Threads[0].Delivery.Status != "answered" {
		t.Fatal(saved)
	}
	a["body"] = "Conflicting retry"
	if _, err := b.comments(a); err == nil {
		t.Fatal("conflicting idempotency key")
	}
}
func TestManagedFailedClaimDoesNotExposeOrRetainAttempt(t *testing.T) {
	b := deliveryFixture(t)
	if err := b.emit("control_returned", "note"); err != nil {
		t.Fatal(err)
	}
	c := openConsumer(t, b, "host")
	b.s.Dir = filepath.Join(t.TempDir(), "missing")
	r, err := b.deliveryAction(obj{"action": "consumer-next", "consumer": c.ID, "instance": c.Instance})
	if err == nil || r != nil || b.s.Notification.Attempt != nil || b.s.Notification.Status != "pending" {
		t.Fatal(r, err, b.s.Notification)
	}
}
func TestManagedTaskWrongRunBindingAndReceipt(t *testing.T) {
	b := deliveryFixture(t)
	b.s.Task = &session.ExecutionTask{ID: "task", Run: "run", Binding: b.s.Binding, Status: "authorized", Delivery: "pending", Expires: time.Now().Add(time.Minute).Format(time.RFC3339Nano)}
	c := openConsumer(t, b, "host")
	e := nextDelivery(t, b, c)
	if e == nil || e.Kind != "task" {
		t.Fatal(e)
	}
	b.s.Task.Delivery = "acknowledged"
	if err := receipt(b, c, e, "unknown"); err != nil {
		t.Fatal(err)
	}
	if b.s.Task.Delivery != "acknowledged" {
		t.Fatal(b.s.Task)
	}
	b.s.RunID = "new-run"
	if err := receipt(b, c, e, "queued"); err == nil {
		t.Fatal("old run receipt")
	}
	b.s.Binding = &session.Binding{ID: "pi", Revision: 2}
	if _, err := b.deliveryAction(obj{"action": "consumer-next", "consumer": c.ID, "instance": c.Instance}); err == nil {
		t.Fatal("old binding")
	}
}

func TestConcurrentManagedConsumersOnlyOneClaim(t *testing.T) {
	b := deliveryFixture(t)
	if err := b.emit("control_returned", "race"); err != nil {
		t.Fatal(err)
	}
	first, second := openConsumer(t, b, "first"), openConsumer(t, b, "second")
	results := make(chan obj, 2)
	failures := make(chan error, 2)
	for _, c := range []session.Consumer{first, second} {
		go func(c session.Consumer) {
			r, e := b.action(obj{"action": "consumer-next", "run": "run", "consumer": c.ID, "instance": c.Instance})
			results <- r
			failures <- e
		}(c)
	}
	count := 0
	for i := 0; i < 2; i++ {
		if r := <-results; r["delivery"] != nil {
			count++
		}
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("claimed %d times", count)
	}
}
