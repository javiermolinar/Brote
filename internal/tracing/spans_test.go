package tracing

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestSharedSpanExportUsesOneCorePipeline(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:1/v1/traces")
	var mu sync.Mutex
	var received []ptrace.Traces
	e := testEngine(t, func(_ context.Context, v ptrace.Traces) error {
		mu.Lock()
		defer mu.Unlock()
		copy := ptrace.NewTraces()
		v.CopyTo(copy)
		received = append(received, copy)
		return nil
	})
	server := httptest.NewUnstartedServer(nil)
	server.Start()
	defer server.Close()
	server.Config.Handler = e.handler(server.URL)
	r := Record{Session: "session:run1", Program: strings.Repeat("a", 32), Debugger: strings.Repeat("b", 32), Name: "demo", Started: time.Now()}
	x := NewSpanExporter(server.URL)
	x.Record = r
	tid, _ := trace.TraceIDFromHex(r.Program)
	sid, _ := trace.SpanIDFromHex(strings.Repeat("c", 16))
	span := tracetest.SpanStub{Name: "capture", SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}), StartTime: time.Now(), EndTime: time.Now(), Resource: resource.Empty(), Attributes: []attribute.KeyValue{attribute.String("program.value.total", "42"), attribute.String("program.capture.id", "capture1")}}.Snapshot()
	if err := x.ExportSpans(context.Background(), []sdk.ReadOnlySpan{span}); err == nil {
		t.Fatal("remote failure must remain visible")
	}
	mu.Lock()
	if len(received) != 1 || received[0].SpanCount() != 1 {
		t.Fatalf("local ingestion lost or duplicated: %d", len(received))
	}
	v, ok := received[0].ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().Get("program.value.total")
	mu.Unlock()
	if !ok || v.Str() != "42" {
		t.Fatal("capture value lost")
	}
	// Close does not manufacture a second schema's run/thread roots.
	_ = x.Shutdown(context.Background())
	e.mu.Lock()
	got := cloneRecord(e.captures[r.Session].Record)
	e.mu.Unlock()
	if !got.Closed || got.ProgramSpans != 1 || got.DebuggerSpans != 0 || got.Local[r.Program] != accepted || got.Remote[r.Program] != failed {
		t.Fatalf("wrong record: %+v", got)
	}
	if _, err := e.spanBatch(SpanBatch{Record: r}); err == nil {
		t.Fatal("explicitly closed capture reopened")
	}
	other := r
	other.Session = "session:run2"
	other.Program = strings.Repeat("d", 32)
	other.Debugger = strings.Repeat("e", 32)
	if _, err := e.spanBatch(SpanBatch{Record: other}); err != nil {
		t.Fatal(err)
	}
	mismatch := other
	mismatch.Program = r.Program
	if _, err := e.spanBatch(SpanBatch{Record: mismatch}); err == nil {
		t.Fatal("changed trace ID accepted")
	}
	e.mu.Lock()
	e.interruptCapture(e.captures[other.Session])
	e.mu.Unlock()
	resumed, err := e.spanBatch(SpanBatch{Record: other})
	if err != nil || resumed.Closed || !resumed.Incomplete || resumed.Program != other.Program {
		t.Fatalf("interrupted producer recovery: %+v %v", resumed, err)
	}
}

func TestSharedSpanEndpointRejectsForeignOriginAndOversize(t *testing.T) {
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	h := e.handler("http://localhost:123")
	for _, tc := range []struct {
		host, body string
		status     int
	}{{"foreign:123", "{}", 403}, {"localhost:123", strings.Repeat(" ", 4<<20) + "{}", 409}} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/api/trace-spans", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status=%d want %d", w.Code, tc.status)
		}
	}
}
