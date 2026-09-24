package tracing

import (
	"agentdebugger/internal/traceinfo"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

func annotationFixture(t *testing.T, e *engine) AnnotationRequest {
	r := metadataFixture(t, e)
	return AnnotationRequest{Session: r.Session, ID: "note", Revision: 1, Author: "human", Body: "observed total", Targets: r.Targets}
}
func TestAnnotationsExplicitOnlyAndRevisions(t *testing.T) {
	var calls atomic.Int32
	e := testEngine(t, func(context.Context, ptrace.Traces) error { calls.Add(1); return nil })
	in := annotationFixture(t, e)
	empty, err := e.listAnnotations(in.Session)
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	first, err := e.annotate(in)
	if err != nil {
		t.Fatal(err)
	}
	flushMetadata(t, e)
	retry, err := e.annotate(in)
	if err != nil || retry.Records[0].SpanID() != first.Records[0].SpanID() {
		t.Fatal(retry, err)
	}
	flushMetadata(t, e)
	if calls.Load() != 1 {
		t.Fatal("retry duplicated export")
	}
	changed := in
	changed.Body = "corrected observation"
	if _, err = e.annotate(changed); err == nil {
		t.Fatal("mutable revision accepted")
	}
	changed.Revision = 2
	if _, err = e.annotate(changed); err != nil {
		t.Fatal(err)
	}
	flushMetadata(t, e)
	list, err := e.listAnnotations(in.Session)
	if err != nil || len(list) != 1 || list[0].Revision != 2 {
		t.Fatal(list, err)
	}
	all, _ := e.readAnnotations(in.Session)
	if len(all) != 2 {
		t.Fatal("revision history lost")
	}
}
func TestAnnotationComparisonValidatedBeforeSaveAndRecovered(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	var count atomic.Int32
	e := testEngine(t, func(context.Context, ptrace.Traces) error {
		if count.Add(1) == 1 {
			return errors.New("temporarily offline")
		}
		return nil
	})
	// Two execution runs within the same durable broker session.
	r1 := Record{Session: "0123456789:one", Program: strings.Repeat("a", 32), Debugger: strings.Repeat("b", 32), Run: strings.Repeat("c", 16), Root: strings.Repeat("d", 16)}
	r2 := Record{Session: "0123456789:two", Program: strings.Repeat("e", 32), Debugger: strings.Repeat("f", 32), Run: strings.Repeat("a", 16), Root: strings.Repeat("b", 16)}
	for _, r := range []Record{r1, r2} {
		if _, err := e.spanBatch(SpanBatch{Record: r, Close: true}); err != nil {
			t.Fatal(err)
		}
	}
	in := AnnotationRequest{Session: r1.Session, ID: "comparison", Revision: 1, Author: "Brote", Body: "Compare captured totals", ComparisonKey: "total-at-three", Targets: []traceinfo.Target{{Session: r1.Session, TraceID: r1.Program, SpanID: r1.Run}, {Session: r2.Session, TraceID: r2.Program, SpanID: r2.Run}}}
	wrong := in
	wrong.Targets = append([]traceinfo.Target{}, in.Targets...)
	wrong.Targets[1].SpanID = strings.Repeat("9", 16)
	if _, err := e.annotate(wrong); err == nil {
		t.Fatal("bad comparison target accepted")
	}
	saved, _ := e.readAnnotations(in.Session)
	if len(saved) != 0 {
		t.Fatal("partially saved invalid pair")
	}
	// Persist the complete pair without publishing, simulating a crash at this boundary.
	a, err := e.prepareAnnotation(in)
	if err != nil || len(a.Records) != 2 {
		t.Fatal(a, err)
	}
	e.syncAnnotations()
	flushMetadata(t, e)
	e.retryMetadata()
	flushMetadata(t, e)
	for _, r := range []Record{r1, r2} {
		list, err := e.listAnnotations(r.Session)
		if err != nil || len(list) != 1 {
			t.Fatal(list, err)
		}
		for _, status := range list[0].Export {
			if status.Local != "accepted" {
				t.Fatal(status)
			}
		}
	}
	if count.Load() != 3 {
		t.Fatalf("expected two deliveries and one retry: %d", count.Load())
	}
}
func TestAnnotationConversationLinkAndBounds(t *testing.T) {
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	r := metadataFixture(t, e)
	if _, err := e.metadata(r); err != nil {
		t.Fatal(err)
	}
	in := AnnotationRequest{Session: r.Session, ID: "note", Revision: 1, Author: "Brote", Body: "finding", Targets: r.Targets, Conversation: &traceinfo.Target{Session: r.Session, TraceID: r.TraceID, SpanID: r.SpanID()}}
	if _, err := e.annotate(in); err != nil {
		t.Fatal(err)
	}
	in.ID = "large"
	in.Body = strings.Repeat("x", traceinfo.MaxBody+1)
	if _, err := e.annotate(in); err == nil {
		t.Fatal("oversized annotation accepted")
	}
	in.Body = "note"
	in.Conversation.SpanID = strings.Repeat("0", 16)
	if _, err := e.annotate(in); err == nil {
		t.Fatal("invented conversation link")
	}
}

func TestAnnotationRejectsOtherInvestigation(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	a := Record{Session: "0123456789:one", Program: strings.Repeat("a", 32), Debugger: strings.Repeat("b", 32), Run: strings.Repeat("c", 16), Root: strings.Repeat("d", 16)}
	b := Record{Session: "1123456789:one", Program: strings.Repeat("e", 32), Debugger: strings.Repeat("f", 32), Run: strings.Repeat("a", 16), Root: strings.Repeat("b", 16)}
	for _, r := range []Record{a, b} {
		if _, err := e.spanBatch(SpanBatch{Record: r, Close: true}); err != nil {
			t.Fatal(err)
		}
	}
	in := AnnotationRequest{Session: a.Session, ID: "wrong", Revision: 1, Author: "human", Body: "note", Targets: []traceinfo.Target{{Session: a.Session, TraceID: a.Program, SpanID: a.Run}, {Session: b.Session, TraceID: b.Program, SpanID: b.Run}}}
	if _, err := e.annotate(in); err == nil {
		t.Fatal("cross-investigation annotation accepted")
	}
	list, err := e.listAnnotations(a.Session)
	if err != nil || len(list) != 0 {
		t.Fatal("rejected annotation persisted", list, err)
	}
}
