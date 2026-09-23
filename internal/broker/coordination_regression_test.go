package broker

import (
	"agentdebugger/internal/session"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegressionExpiredHostCannotReviveBeforeMaintenance(t *testing.T) {
	b, _ := coordinationFixture(t)
	b.s.ID = "0123456789"
	b.s.ServiceVersion = 1
	b.s.RunID = "run"
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	c := openConsumer(t, b, "host")
	hostTask(b)
	if err := hostFact(t, b, c, 1, "turn", "active", true); err != nil {
		t.Fatal(err)
	}
	a := obj{"action": "task-heartbeat", "task": "task", "binding": "pi", "consumer": c.ID, "instance": c.Instance, "turn": "turn"}
	if _, err := b.coordinate(a); err != nil {
		t.Fatal(err)
	}
	old := b.s.Consumers[c.ID]
	old.Host.Expires = time.Now().Add(-time.Millisecond).Format(time.RFC3339Nano)
	b.s.Consumers[c.ID] = old
	// A liveness response races ahead of the once-per-second maintenance tick.
	if err := hostFact(t, b, c, 2, "turn", "active", true); err != nil {
		return
	}
	if err := b.taskValid(obj{"task": "task", "binding": "pi"}); err == nil {
		t.Fatal("expired host proof was refreshed and old execution scope remains valid")
	}
}
func regressionQuestion(t *testing.T, b *broker) {
	t.Helper()
	d := session.Discussion{Session: b.s.ID, Threads: []session.CommentThread{{ID: "thread", Messages: []session.CommentMessage{{ID: "q", Body: "Why?", Author: "human"}}, Delivery: session.CommentDelivery{Question: "q", Status: "pending", Binding: b.s.Binding}}}}
	if err := session.WriteDiscussion(d); err != nil {
		t.Fatal(err)
	}
}
func TestRegressionRetriedQuestionRejectsPreviousAttemptBeforeClaim(t *testing.T) {
	b := deliveryFixture(t)
	regressionQuestion(t, b)
	c := openConsumer(t, b, "host")
	e := nextDelivery(t, b, c)
	if err := receipt(b, c, e, "unknown"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.comments(obj{"action": "retry", "thread": "thread"}); err != nil {
		t.Fatal(err)
	}
	_, err := b.comments(obj{"action": "reply", "thread": "thread", "question": "q", "binding": "pi", "revision": 1, "messageId": "old-answer", "body": "obsolete answer", "attempt": e.Attempt})
	if err == nil {
		t.Fatal("previous attempt answered a pending replacement before it was claimed")
	}
}
func TestRegressionDurableCrashBoundaries(t *testing.T) {
	for _, kind := range []string{"handback", "question", "task"} {
		t.Run(kind, func(t *testing.T) {
			b := deliveryFixture(t)
			switch kind {
			case "handback":
				if err := b.emit("control_returned", "note"); err != nil {
					t.Fatal(err)
				}
			case "question":
				regressionQuestion(t, b)
			case "task":
				hostTask(b)
			}
			c := openConsumer(t, b, "host")
			// Pending durable work remains discoverable after journal expiry.
			for i := 0; i < 260; i++ {
				if err := b.emit("inspection", ""); err != nil {
					t.Fatal(err)
				}
			}
			e := nextDelivery(t, b, c)
			if e == nil {
				t.Fatal("pending work lost after journal expiry")
			}
			restore := func() {
				data, err := os.ReadFile(filepath.Join(b.s.Dir, "session.json"))
				if err != nil {
					t.Fatal(err)
				}
				var s session.Descriptor
				if err = json.Unmarshal(data, &s); err != nil {
					t.Fatal(err)
				}
				b.s = s
			}
			restore() // crash after claim persistence, before sender receipt
			if nextDelivery(t, b, c) != nil {
				t.Fatal("claim replayed")
			}
			if err := receipt(b, c, e, "acknowledged"); err != nil {
				t.Fatal(err)
			}
			restore() // crash after acknowledgement persistence, cursor still lagging
			for _, status := range []string{"queued", "unknown", "failed", "acknowledged"} {
				if err := receipt(b, c, e, status); err != nil {
					t.Fatal(err)
				}
			}
			if nextDelivery(t, b, c) != nil {
				t.Fatal("acknowledged work replayed")
			}
		})
	}
}
func TestRegressionReceiptPersistenceFailure(t *testing.T) {
	b := deliveryFixture(t)
	if err := b.emit("control_returned", "note"); err != nil {
		t.Fatal(err)
	}
	c := openConsumer(t, b, "host")
	e := nextDelivery(t, b, c)
	original := b.s.Dir
	b.s.Dir = filepath.Join(t.TempDir(), "missing")
	if err := receipt(b, c, e, "queued"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if b.s.Notification.Status != "sending" {
		t.Fatal("failed write retained receipt")
	}
	b.s.Dir = original
	if err := receipt(b, c, e, "queued"); err != nil {
		t.Fatal(err)
	}
	replacement := openConsumer(t, b, "host")
	if nextDelivery(t, b, replacement) != nil {
		t.Fatal("queued delivery reinjected")
	}
}

func TestRegressionHumanPauseRacesFreshHostFact(t *testing.T) {
	for i := 0; i < 10; i++ {
		t.Run(time.Now().Format("150405.000000000"), func(t *testing.T) {
			b, _ := coordinationFixture(t)
			b.s.ID = "0123456789"
			b.s.ServiceVersion = 1
			b.s.RunID = "run"
			t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
			t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
			c := openConsumer(t, b, "host")
			hostTask(b)
			if err := hostFact(t, b, c, 1, "turn", "active", true); err != nil {
				t.Fatal(err)
			}
			if _, err := b.coordinate(obj{"action": "task-heartbeat", "task": "task", "binding": "pi", "consumer": c.ID, "instance": c.Instance, "turn": "turn"}); err != nil {
				t.Fatal(err)
			}
			challenge, err := b.deliveryAction(obj{"action": "consumer-challenge", "consumer": c.ID, "instance": c.Instance})
			if err != nil {
				t.Fatal(err)
			}
			results := make(chan error, 2)
			start := make(chan struct{})
			go func() {
				<-start
				_, err := b.action(obj{"action": "pause", "actor": "human", "run": "run", "generation": -1})
				results <- err
			}()
			go func() {
				<-start
				_, err := b.action(obj{"action": "consumer-fact", "consumer": c.ID, "instance": c.Instance, "run": "run", "fact": obj{"instance": c.Instance, "sequence": 2, "turn": "turn", "state": "active", "challenge": challenge["challenge"]}})
				results <- err
			}()
			close(start)
			for j := 0; j < 2; j++ {
				if err := <-results; err != nil {
					t.Fatal(err)
				}
			}
			if b.s.Task.Status != "cancelled" {
				t.Fatal("Pause lost lifecycle race")
			}
			if err := b.taskValid(obj{"task": "task", "binding": "pi"}); err == nil {
				t.Fatal("cancelled scope accepted")
			}
		})
	}
}

func TestClosedSenderExposesUnknownToDifferentReader(t *testing.T) {
	b := deliveryFixture(t)
	regressionQuestion(t, b)
	c := openConsumer(t, b, "original")
	e := nextDelivery(t, b, c)
	if _, err := b.deliveryAction(obj{"action": "consumer-close", "consumer": c.ID, "instance": c.Instance}); err != nil {
		t.Fatal(err)
	}
	d, err := session.ReadDiscussion(b.s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Threads[0].Delivery.Status != "unknown" {
		t.Fatal("closed sender stranded question")
	}
	other := openConsumer(t, b, "different")
	if nextDelivery(t, b, other) != nil {
		t.Fatal("ambiguous send replayed")
	}
	if err := receipt(b, c, e, "queued"); err == nil {
		t.Fatal("closed sender receipt accepted")
	}
	if _, err := b.comments(obj{"action": "retry", "thread": "thread"}); err != nil {
		t.Fatal(err)
	}
}
