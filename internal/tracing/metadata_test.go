package tracing

import (
	"agentdebugger/internal/session"
	"agentdebugger/internal/traceinfo"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

func metadataFixture(t *testing.T, e *engine) traceinfo.Record {
	t.Helper()
	r := Record{Session: "s:run", Program: strings.Repeat("a", 32), Debugger: strings.Repeat("b", 32), Run: strings.Repeat("c", 16), Root: strings.Repeat("d", 16), Name: "demo"}
	if _, err := e.spanBatch(SpanBatch{Record: r, Close: true}); err != nil {
		t.Fatal(err)
	}
	return traceinfo.Record{ID: "question", Revision: 1, Kind: "conversation", Session: r.Session, TraceID: r.Debugger, ParentID: r.Root, Created: time.Now().UTC(), Author: "human", Body: "Why?", Thread: "t1", Message: "q1", Targets: []traceinfo.Target{{Session: r.Session, TraceID: r.Program, SpanID: r.Run}}}
}
func flushMetadata(t *testing.T, e *engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.flush(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestClosedRunMetadataDedupAndLinks(t *testing.T) {
	var mu sync.Mutex
	var got []ptrace.Traces
	e := testEngine(t, func(_ context.Context, v ptrace.Traces) error {
		mu.Lock()
		defer mu.Unlock()
		copy := ptrace.NewTraces()
		v.CopyTo(copy)
		got = append(got, copy)
		return nil
	})
	r := metadataFixture(t, e)
	if _, err := e.metadata(r); err != nil {
		t.Fatal(err)
	}
	flushMetadata(t, e)
	if _, err := e.metadata(r); err != nil {
		t.Fatal(err)
	}
	flushMetadata(t, e)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("duplicate batches: %d", len(got))
	}
	span := got[0].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	if span.Name() != "brote.conversation" || span.TraceID().String() != r.TraceID || span.SpanID().String() != r.SpanID() || span.Links().Len() != 1 {
		t.Fatal("wrong routing or links")
	}
	e.mu.Lock()
	c := e.captures[r.Session]
	closed, count := c.Closed, c.DebuggerSpans
	e.mu.Unlock()
	if !closed || count != 1 {
		t.Fatalf("changed execution or count: %v %d", closed, count)
	}
	r.Body = "different"
	if _, err := e.metadata(r); err == nil {
		t.Fatal("conflicting retry accepted")
	}
}
func TestMetadataRecoversFailedDestinationAndKeepsSpanIdentity(t *testing.T) {
	var calls atomic.Int32
	e := testEngine(t, func(context.Context, ptrace.Traces) error {
		if calls.Add(1) == 1 {
			return errors.New("offline")
		}
		return nil
	})
	r := metadataFixture(t, e)
	if _, err := e.metadata(r); err != nil {
		t.Fatal(err)
	}
	flushMetadata(t, e)
	// Simulate losing only the in-memory state; recovery reads the durable receipt.
	e.mu.Lock()
	delete(e.captures, r.Session)
	e.mu.Unlock()
	e.retryMetadata()
	flushMetadata(t, e)
	e.mu.Lock()
	c := e.captures[r.Session]
	status, count := c.Metadata[r.SpanID()].Local, c.DebuggerSpans
	e.mu.Unlock()
	if status != "accepted" || calls.Load() != 2 || count != 1 {
		t.Fatalf("retry status %s calls %d count %d", status, calls.Load(), count)
	}
	e.retryMetadata()
	flushMetadata(t, e)
	if calls.Load() != 2 {
		t.Fatal("replayed accepted destination")
	}
}
func TestMetadataRemoteRetryDoesNotDuplicateLocal(t *testing.T) {
	var remote, local atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if remote.Add(1) == 1 {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", server.URL)
	e := testEngine(t, func(context.Context, ptrace.Traces) error { local.Add(1); return nil })
	r := metadataFixture(t, e)
	if _, err := e.metadata(r); err != nil {
		t.Fatal(err)
	}
	flushMetadata(t, e)
	e.retryMetadata()
	flushMetadata(t, e)
	if local.Load() != 1 || remote.Load() != 2 {
		t.Fatalf("local %d remote %d", local.Load(), remote.Load())
	}
}
func TestMetadataRejectsWrongTraceAndMissingCapture(t *testing.T) {
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	r := metadataFixture(t, e)
	r.Targets[0].CaptureID = "not-captured"
	if _, err := e.metadata(r); err == nil {
		t.Fatal("invented capture accepted")
	}
	r.Targets[0].CaptureID = ""
	r.TraceID = r.Targets[0].TraceID
	if _, err := e.metadata(r); err == nil {
		t.Fatal("conversation on program trace")
	}
	r.Kind = "annotation"
	r.ParentID = r.Targets[0].SpanID
	if _, err := e.metadata(r); err != nil {
		t.Fatal(err)
	}
}

func TestCommittedConversationSourceSurvivesBrokerExit(t *testing.T) {
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	var calls atomic.Int32
	e := testEngine(t, func(context.Context, ptrace.Traces) error { calls.Add(1); return nil })
	r := metadataFixture(t, e)
	d := session.Discussion{Session: "0123456789", TraceOwner: &session.TraceOwner{Session: r.Session, Debugger: r.TraceID, Root: r.ParentID, Program: r.Targets[0].TraceID, Run: r.Targets[0].SpanID}, Threads: []session.CommentThread{{ID: "thread", Messages: []session.CommentMessage{{ID: "question", Author: "human", Body: "why?", Created: time.Now().UTC().Format(time.RFC3339Nano)}}}}}
	if err := session.CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	e.syncSources()
	flushMetadata(t, e)
	e.syncSources()
	flushMetadata(t, e)
	if calls.Load() != 1 {
		t.Fatalf("source replay duplicated conversation: %d", calls.Load())
	}
	d.Threads[0].Messages = append(d.Threads[0].Messages, session.CommentMessage{ID: "answer", Author: "Brote", Body: "saved reply", Question: "question", Created: time.Now().UTC().Format(time.RFC3339Nano)})
	if err := session.CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	e.syncSources()
	flushMetadata(t, e)
	if calls.Load() != 2 {
		t.Fatalf("late reply missing: %d", calls.Load())
	}
}

func TestMetadataKeepsOriginalRootAfterReconnect(t *testing.T) {
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	r := metadataFixture(t, e)
	e.mu.Lock()
	c := e.captures[r.Session]
	c.Root = strings.Repeat("e", 16)
	c.Run = strings.Repeat("f", 16)
	if err := e.save(c); err != nil {
		t.Fatal(err)
	}
	e.mu.Unlock()
	if _, err := e.metadata(r); err != nil {
		t.Fatal("old metadata roots lost", err)
	}
}

func TestNativeCaptureIdentityCanBeAnnotated(t *testing.T) {
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	if _, err := e.event(Event{Session: "native-editor", Name: "demo", Kind: "start"}); err != nil {
		t.Fatal(err)
	}
	r, err := e.event(Event{Session: "native-editor", Kind: "snapshot", Observation: &Observation{Thread: 1, CapturedAt: time.Now(), Frame: Frame{Name: "main.work"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Captures) != 1 {
		t.Fatal("capture identity missing", r.Captures)
	}
	var target traceinfo.Target
	for id, span := range r.Captures {
		target = traceinfo.Target{Session: r.Session, TraceID: r.Program, SpanID: span, CaptureID: id}
	}
	if _, err := e.event(Event{Session: r.Session, Kind: "close"}); err != nil {
		t.Fatal(err)
	}
	note, err := e.annotate(AnnotationRequest{Session: r.Session, ID: "native-note", Revision: 1, Author: "human", Body: "saved native observation", Targets: []traceinfo.Target{target}})
	if err != nil || len(note.Records) != 1 || note.Records[0].Targets[0].SpanID != target.SpanID {
		t.Fatal(note, err)
	}
}

func TestMetadataEndpointRejectsCrossInvestigationAnnotations(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	local := metadataFixture(t, e)
	other := Record{Session: "other:run", Program: strings.Repeat("1", 32), Debugger: strings.Repeat("2", 32), Run: strings.Repeat("3", 16), Root: strings.Repeat("4", 16)}
	if _, err := e.spanBatch(SpanBatch{Record: other, Close: true}); err != nil {
		t.Fatal(err)
	}
	foreign := traceinfo.Record{ID: "foreign-question", Revision: 1, Kind: "conversation", Session: other.Session, TraceID: other.Debugger, ParentID: other.Root, Created: time.Now().UTC(), Author: "human", Body: "why?"}
	if _, err := e.metadata(foreign); err != nil {
		t.Fatal(err)
	}
	note := traceinfo.Record{ID: "explicit-note", Revision: 1, Kind: "annotation", Session: local.Session, TraceID: local.Targets[0].TraceID, ParentID: local.Targets[0].SpanID, Created: time.Now().UTC(), Author: "human", Body: "note", Targets: local.Targets}
	for _, conversation := range []bool{false, true} {
		in := note
		if conversation {
			in.Conversation = &traceinfo.Target{Session: other.Session, TraceID: other.Debugger, SpanID: foreign.SpanID()}
		} else {
			in.Targets = append(append([]traceinfo.Target{}, in.Targets...), traceinfo.Target{Session: other.Session, TraceID: other.Program, SpanID: other.Run})
		}
		payload, _ := json.Marshal(in)
		r := httptest.NewRequest("POST", "http://127.0.0.1:9876/api/trace-metadata", bytes.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.handler("http://127.0.0.1:9876").ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatal("cross-investigation metadata accepted", conversation, w.Body.String())
		}
		e.mu.Lock()
		_, saved := e.captures[local.Session].Metadata[in.SpanID()]
		e.mu.Unlock()
		if saved {
			t.Fatal("invalid note persisted")
		}
	}
	if _, err := e.metadata(note); err != nil {
		t.Fatal("valid same-run annotation rejected", err)
	}
}
