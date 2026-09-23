package protocol

import "testing"

func TestDeliveryAuthorityIsSeparateFromHistoricalEvidence(t *testing.T) {
	for _, kind := range []string{"agent", "provider"} {
		e := DeliveryEnvelope{Version: 1, Session: "session", Kind: "question", Subject: "question", Thread: "thread", Attempt: "attempt", Recipient: Recipient{Kind: kind, ID: "host", Revision: 2}, EvidenceRun: "old-run"}
		if err := e.Check(); err != nil {
			t.Fatal(err)
		}
		e.Kind = "task"
		if err := e.Check(); err == nil {
			t.Fatal("execution subject accepted without run")
		}
		e.Run = "current-run"
		if err := e.Check(); (err == nil) != (kind == "agent") {
			t.Fatalf("task recipient %s: %v", kind, err)
		}
	}
}
func TestCoordinationContractRejectsIncompleteIdentity(t *testing.T) {
	e := DeliveryEnvelope{Version: 1, Session: "s", Run: "r", Kind: "handback", Subject: "event", Attempt: "a", Recipient: Recipient{Kind: "agent", ID: "b", Revision: 1}}
	if err := e.Check(); err != nil {
		t.Fatal(err)
	}
	e.Version = 2
	if e.Check() == nil {
		t.Fatal("future protocol")
	}
	e.Version = 1
	e.Attempt = ""
	if e.Check() == nil {
		t.Fatal("missing attempt")
	}
	for _, f := range []HostFact{{}, {Instance: "h", Sequence: 1, State: "active"}, {Instance: "h", Sequence: 1, State: "queued"}} {
		if f.Check() == nil {
			t.Fatal(f)
		}
	}
	for _, state := range []string{"active", "idle", "closed"} {
		if err := (HostFact{Instance: "h", Sequence: 1, State: state, Turn: "t"}).Check(); err != nil {
			t.Fatal(err)
		}
	}
}
