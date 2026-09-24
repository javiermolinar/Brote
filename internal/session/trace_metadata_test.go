package session

import (
	"agentdebugger/internal/traceinfo"
	"errors"
	"strings"
	"testing"
	"time"
)

func traceDiscussion(t *testing.T) Discussion {
	t.Helper()
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	return Discussion{Session: "0123456789", TraceOwner: &TraceOwner{Session: "0123456789:run1", Program: strings.Repeat("a", 32), Debugger: strings.Repeat("b", 32), Root: strings.Repeat("c", 16), Run: strings.Repeat("d", 16)}, Threads: []CommentThread{{ID: "t1", Messages: []CommentMessage{{ID: "q1", Author: "human", Body: "why?", Created: time.Now().UTC().Format(time.RFC3339Nano)}}}}}
}
func TestConversationTraceCommittedWithMessage(t *testing.T) {
	d := traceDiscussion(t)
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadTraceMetadata(d.Session)
	if err != nil || len(saved) != 1 {
		t.Fatal(saved, err)
	}
	if saved[0].TraceID != d.TraceOwner.Debugger || saved[0].Targets[0].TraceID != d.TraceOwner.Program {
		t.Fatal("wrong routing")
	}
	stale, _ := ReadDiscussion(d.Session)
	d.Threads[0].Messages = append(d.Threads[0].Messages, CommentMessage{ID: "answer", Author: "Brote", Question: "q1", Body: strings.Repeat("界", 16000), Created: time.Now().UTC().Format(time.RFC3339Nano)})
	if err = CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	stale.Threads[0].Messages = append(stale.Threads[0].Messages, CommentMessage{ID: "loser", Author: "human", Body: "not committed", Created: time.Now().UTC().Format(time.RFC3339Nano)})
	if !errors.Is(CommitDiscussion(&stale), ErrDiscussionConflict) {
		t.Fatal("expected conflict")
	}
	records, _ := ReadTraceMetadata(d.Session)
	if len(records) != 2 || !records[1].Truncated || len(records[1].Body) > traceinfo.MaxBody || records[1].Targets[0] != records[0].Targets[0] {
		t.Fatal(records)
	}
	ids, err := TraceSources()
	if err != nil || len(ids) != 1 || ids[0] != d.Session {
		t.Fatal(ids, err)
	}
	d.Threads[0].Messages[0].Body = "rewrite"
	if CommitDiscussion(&d) == nil {
		t.Fatal("rewrote exported message")
	}
}
func TestConversationTraceContinuedEvidenceStaysInOldRun(t *testing.T) {
	d := traceDiscussion(t)
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	previous := d.Threads[0].Messages[0].Trace
	next := Discussion{Session: "1123456789", TraceOwner: &TraceOwner{Session: "1123456789:run2", Debugger: strings.Repeat("e", 32), Program: strings.Repeat("f", 32), Root: strings.Repeat("a", 16), Run: strings.Repeat("b", 16)}, Threads: d.Threads}
	next.Threads[0].Messages = append(next.Threads[0].Messages, CommentMessage{ID: "reply", Question: "q1", Author: "Brote", Body: "after restart", Created: time.Now().UTC().Format(time.RFC3339Nano)})
	if err := CommitDiscussion(&next); err != nil {
		t.Fatal(err)
	}
	records, _ := ReadTraceMetadata(next.Session)
	if len(records) != 1 || records[0].TraceID != next.TraceOwner.Debugger || records[0].Targets[0].TraceID != previous.Targets[0].TraceID {
		t.Fatal("lost old evidence", records)
	}
}
func TestLegacyDiscussionNotBackfilled(t *testing.T) {
	d := traceDiscussion(t)
	owner := d.TraceOwner
	d.TraceOwner = nil
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	d.TraceOwner = owner
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	records, _ := ReadTraceMetadata(d.Session)
	if len(records) != 0 {
		t.Fatal("exported old history")
	}
}

func TestLegacyEvidenceWithoutTraceDoesNotLinkCurrentRun(t *testing.T) {
	d := traceDiscussion(t)
	d.Threads[0].Messages[0].Context = map[string]any{"session": "1123456789", "run": "old", "source": "legacy.go"}
	if err := CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	if len(d.Threads[0].Messages[0].Trace.Targets) != 0 {
		t.Fatal("invented current-run link for old evidence")
	}
}
