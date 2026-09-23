package delivery

import (
	"agentdebugger/internal/protocol"
	"testing"
)

func TestAcknowledgementWinsLateReceipt(t *testing.T) {
	for _, ack := range []string{"thinking", "acknowledged", "answered"} {
		for _, late := range []string{"queued", "failed", "unknown"} {
			got, err := Transition(ack, late)
			if err != nil || got != ack {
				t.Fatalf("%s + %s: %s %v", ack, late, got, err)
			}
		}
	}
}
func TestClaimAndAmbiguousRecovery(t *testing.T) {
	s, err := Transition("pending", "sending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Transition(s, "sending"); err == nil {
		t.Fatal("competing claim")
	}
	s = Interrupted(s)
	if s != "unknown" {
		t.Fatal(s)
	}
	if _, err := Transition(s, "sending"); err == nil {
		t.Fatal("automatically replayed uncertain send")
	}
	if s, err = Transition(s, "thinking"); err != nil || s != "thinking" {
		t.Fatal(s, err)
	}
}
func TestAttemptFencesConsumerAndRecipient(t *testing.T) {
	r := protocol.Recipient{Kind: "agent", ID: "b", Revision: 2}
	a := Attempt{ID: "attempt", Consumer: "consumer", Instance: "instance", Recipient: r}
	if err := a.Check("attempt", "consumer", "instance", r); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][3]string{{"old", "consumer", "instance"}, {"attempt", "other", "instance"}, {"attempt", "consumer", "old"}} {
		if a.Check(args[0], args[1], args[2], r) == nil {
			t.Fatal(args)
		}
	}
	r.Revision++
	if a.Check("attempt", "consumer", "instance", r) == nil {
		t.Fatal("obsolete binding")
	}
}
