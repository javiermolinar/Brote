package broker

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"testing"
	"time"
)

func hostFact(t *testing.T, b *broker, c session.Consumer, seq uint64, turn, state string, proof bool) error {
	t.Helper()
	f := protocol.HostFact{Instance: c.Instance, Sequence: seq, Turn: turn, State: state}
	if proof {
		r, err := b.deliveryAction(obj{"action": "consumer-challenge", "consumer": c.ID, "instance": c.Instance})
		if err != nil {
			t.Fatal(err)
		}
		f.Challenge = str(r["challenge"])
	}
	_, err := b.deliveryAction(obj{"action": "consumer-fact", "consumer": c.ID, "instance": c.Instance, "fact": f})
	return err
}
func hostTask(b *broker) {
	b.s.Task = &session.ExecutionTask{ID: "task", Binding: b.s.Binding, Status: "authorized", Delivery: "pending", Run: b.s.RunID, Expires: time.Now().Add(time.Minute).Format(time.RFC3339Nano)}
}
func TestHostProofNotListenerPresenceControlsScope(t *testing.T) {
	b := deliveryFixture(t)
	c := openConsumer(t, b, "host")
	hostTask(b)
	a := obj{"action": "task-heartbeat", "task": "task", "binding": "pi", "consumer": c.ID, "instance": c.Instance, "turn": "turn"}
	if _, err := b.coordinate(a); err == nil {
		t.Fatal("listener alone claimed host task")
	}
	if err := hostFact(t, b, c, 1, "turn", "active", false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.coordinate(a); err == nil {
		t.Fatal("unchallenged active fact claimed task")
	}
	if err := hostFact(t, b, c, 2, "turn", "active", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.coordinate(a); err != nil {
		t.Fatal(err)
	}
	if !b.hostTaskValid(b.s.Task, time.Now()) {
		t.Fatal("valid host scope rejected")
	}
	old := b.s.Consumers[c.ID]
	old.Host.Expires = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	b.s.Consumers[c.ID] = old
	if err := b.taskValid(obj{"task": "task", "binding": "pi"}); err == nil || b.s.Task.Status != "cancelled" {
		t.Fatal("host loss did not cancel", err)
	}
	if err := hostFact(t, b, c, 3, "turn", "active", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.coordinate(a); err == nil {
		t.Fatal("host proof revived cancelled task")
	}
}
func TestHostSequenceTurnAndInstanceFencing(t *testing.T) {
	b, _ := coordinationFixture(t)
	b.s.ID = "0123456789"
	b.s.ServiceVersion = 1
	b.s.RunID = "run"
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	c := openConsumer(t, b, "host")
	hostTask(b)
	if err := hostFact(t, b, c, 3, "turn", "active", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.coordinate(obj{"action": "task-heartbeat", "task": "task", "binding": "pi", "consumer": c.ID, "instance": c.Instance, "turn": "turn"}); err != nil {
		t.Fatal(err)
	}
	if err := hostFact(t, b, c, 2, "turn", "idle", false); err == nil {
		t.Fatal("out-of-order idle")
	}
	if b.s.Task.Status == "cancelled" {
		t.Fatal("stale fact cancelled task")
	}
	if err := hostFact(t, b, c, 4, "other-turn", "active", true); err != nil {
		t.Fatal(err)
	}
	if b.s.Task.Status != "cancelled" {
		t.Fatal("new turn adopted task")
	}
	replacement := openConsumer(t, b, "host")
	if replacement.Instance == c.Instance {
		t.Fatal("instance not fenced")
	}
	if err := hostFact(t, b, c, 5, "turn", "active", false); err == nil {
		t.Fatal("old instance")
	}
}
func TestHostIdleCompletesSettledTaskAndObserverCloseDoesNotCancel(t *testing.T) {
	b, _ := coordinationFixture(t)
	b.s.ID = "0123456789"
	b.s.ServiceVersion = 1
	b.s.RunID = "run"
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	c := openConsumer(t, b, "host")
	observer := openConsumer(t, b, "observer")
	hostTask(b)
	if err := hostFact(t, b, c, 1, "turn", "active", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.coordinate(obj{"action": "task-heartbeat", "task": "task", "binding": "pi", "consumer": c.ID, "instance": c.Instance, "turn": "turn"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.deliveryAction(obj{"action": "consumer-close", "consumer": observer.ID, "instance": observer.Instance}); err != nil {
		t.Fatal(err)
	}
	if b.s.Task.Status == "cancelled" {
		t.Fatal("observer cancelled host")
	}
	if err := hostFact(t, b, c, 2, "turn", "idle", false); err != nil {
		t.Fatal(err)
	}
	if b.s.Task.Status != "completed" {
		t.Fatal(b.s.Task)
	}
}
func TestHumanPauseCancelsHostBeforeNewFacts(t *testing.T) {
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
	if _, err := b.action(obj{"action": "pause", "actor": "human", "run": "run", "generation": -1}); err != nil {
		t.Fatal(err)
	}
	if err := hostFact(t, b, c, 2, "turn", "active", true); err != nil {
		t.Fatal(err)
	}
	if b.s.Task.Status != "cancelled" {
		t.Fatal("human pause undone")
	}
}

func TestRenewalRequiresAcknowledgedTaskAndFreshTurnProof(t *testing.T) {
	b, _ := coordinationFixture(t)
	b.s.ID = "0123456789"
	b.s.ServiceVersion = 1
	b.s.RunID = "run"
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	c := openConsumer(t, b, "host")
	hostTask(b)
	b.agentStreams = 4
	now := time.Now()
	original := now.Add(20 * time.Second).UTC().Format(time.RFC3339Nano)
	b.s.Task.Expires = original
	b.maintainTask(now)
	if b.s.Task.Expires != original {
		t.Fatal("listener renewed task")
	}
	if err := hostFact(t, b, c, 1, "turn", "active", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.coordinate(obj{"action": "task-heartbeat", "task": "task", "binding": "pi", "consumer": c.ID, "instance": c.Instance, "turn": "turn"}); err != nil {
		t.Fatal(err)
	}
	b.s.Task.Expires = original
	b.maintainTask(now)
	expiry, _ := time.Parse(time.RFC3339Nano, b.s.Task.Expires)
	if !expiry.Equal(now.Add(taskLease)) {
		t.Fatal("Go did not renew acknowledged active scope", expiry)
	}
	b.maintainTask(now.Add(hostProofTTL + time.Second))
	if b.s.Task.Status != "cancelled" {
		t.Fatal("stale host proof kept scope alive")
	}
}
