package session

import (
	"agentdebugger/internal/protocol"
	"errors"
	"strings"
	"sync"
	"testing"
)

func discussionFixture(t *testing.T) Discussion {
	t.Helper()
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	r := protocol.Recipient{Kind: "provider", ID: "model", Revision: 1, Name: "Model"}
	d := Discussion{Session: "0123456789", Threads: []CommentThread{{ID: "thread", Context: map[string]any{"partial": true, "error": "scope unavailable", "sourceIdentity": "hash", "traceId": "trace"}, Messages: []CommentMessage{{ID: "q", Author: "human", Body: "question"}}, Delivery: CommentDelivery{Question: "q", Status: "pending", Recipient: &r}}}}
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
func TestDiscussionCASAndEvidenceProvenance(t *testing.T) {
	d := discussionFixture(t)
	stale, _ := ReadDiscussion(d.Session)
	d.Threads[0].Resolved = true
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	if err := CommitDiscussion(&stale); !errors.Is(err, ErrDiscussionConflict) {
		t.Fatal("stale writer", err)
	}
	got, _ := ReadDiscussion(d.Session)
	m := got.Threads[0].Messages[0]
	if m.Evidence == nil || m.Evidence.Session != d.Session || m.Evidence.ExecutionRun != "" || m.Context["traceId"] != "trace" {
		t.Fatal("lost or invented provenance", m)
	}
	got.Threads[0].Messages[0].Context["run"] = "new-run"
	if err := CommitDiscussion(&got); err == nil {
		t.Fatal("mutated immutable evidence")
	}
}
func TestOfflineAnswerLosslessIdempotentAndConcurrent(t *testing.T) {
	d := discussionFixture(t)
	r := *d.Threads[0].Delivery.Recipient
	body := strings.Repeat("界", 32000)
	a := DiscussionRequest{Action: "reply", Thread: "thread", Question: "q", MessageID: "answer", Body: body, Recipient: &r}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := MutateDiscussion(d.Session, a); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _ := ReadDiscussion(d.Session)
	if len(got.Threads[0].Messages) != 2 || got.Threads[0].Messages[1].Body != body {
		t.Fatal("lost or duplicated answer")
	}
	a.Body = "different"
	if _, err := MutateDiscussion(d.Session, a); err == nil {
		t.Fatal("conflicting key")
	}
	a.Body = body
	a.Question = "old"
	if _, err := MutateDiscussion(d.Session, a); err == nil {
		t.Fatal("obsolete question")
	}
}
func TestRetryRejectsOldAttemptBeforeNewClaim(t *testing.T) {
	d := discussionFixture(t)
	d.Threads[0].Delivery.Status = "unknown"
	d.Threads[0].Delivery.AttemptRequired = true
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateDiscussion(d.Session, DiscussionRequest{Action: "retry", Thread: "thread"}); err != nil {
		t.Fatal(err)
	}
	a := DiscussionRequest{Action: "reply", Thread: "thread", Question: "q", MessageID: "answer", Body: "old answer", Attempt: "old", Recipient: d.Threads[0].Delivery.Recipient}
	if _, err := MutateDiscussion(d.Session, a); err == nil {
		t.Fatal("old attempt accepted")
	}
	a.Attempt = ""
	if _, err := MutateDiscussion(d.Session, a); err == nil {
		t.Fatal("missing attempt accepted")
	}
}

func TestEvidenceHashSurvivesTypedValuesAndLargeAddresses(t *testing.T) {
	d := discussionFixture(t)
	type value struct {
		Z uint64 `json:"z"`
		A string `json:"a"`
	}
	d.Threads[0].Messages = append(d.Threads[0].Messages, CommentMessage{ID: "typed", Author: "human", Body: "typed", Context: map[string]any{"value": value{Z: 18446744073709551614, A: "address"}}})
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDiscussion(d.Session)
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitDiscussion(&got); err != nil {
		t.Fatal("round trip changed immutable evidence", err)
	}
}
