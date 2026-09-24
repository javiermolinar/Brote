package telemetry

import (
	"context"
	"errors"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"sync"
	"testing"
	"time"
)

type memoryExporter struct {
	mu    sync.Mutex
	spans []sdk.ReadOnlySpan
	fail  bool
	block chan struct{}
}

func (m *memoryExporter) ExportSpans(ctx context.Context, spans []sdk.ReadOnlySpan) error {
	if m.block != nil {
		select {
		case <-m.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if m.fail {
		return errors.New("private transport details")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spans = append(m.spans, spans...)
	return nil
}
func (*memoryExporter) Shutdown(context.Context) error { return nil }
func TestTraceParentageValuesAndDelivery(t *testing.T) {
	sink := &memoryExporter{}
	s := newSession(sink, "session", "run", "fixture", IDs{})
	when := time.Now()
	status := s.Capture("capture", "work.entry", 1, 7, map[string]any{"stack": []any{}}, map[string]any{"input": map[string]any{"type": "int", "value": "42"}}, when)
	if status != "queued" && status != "sent" {
		t.Fatal(status)
	}
	s.Action("continue", "acknowledged")
	s.Close()
	if s.Status("capture") != "sent" {
		t.Fatal("capture not sent")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	byID := map[string]sdk.ReadOnlySpan{}
	var capture sdk.ReadOnlySpan
	for _, span := range sink.spans {
		byID[span.SpanContext().SpanID().String()] = span
		if captureID(span) == "capture" {
			capture = span
		}
	}
	if capture == nil || !capture.StartTime().Equal(capture.EndTime()) {
		t.Fatal("snapshot represented as function duration")
	}
	if s.CaptureSpanID("capture") != capture.SpanContext().SpanID().String() {
		t.Fatal("capture reference differs from exported span")
	}
	parent := byID[capture.Parent().SpanID().String()]
	if parent == nil || byID[parent.Parent().SpanID().String()] == nil {
		t.Fatal("missing observation hierarchy")
	}
	found := false
	for _, attr := range capture.Attributes() {
		if string(attr.Key) == "program.value.input" && attr.Value.AsString() == "42" {
			found = true
		}
	}
	if !found {
		t.Fatal("selected value missing")
	}
	if capture.SpanContext().TraceID().String() != s.IDs.Program || s.IDs.Program == s.IDs.Debugger {
		t.Fatal("trace identity mismatch")
	}
}
func TestExporterFailureAndCapacity(t *testing.T) {
	sink := &memoryExporter{block: make(chan struct{})}
	s := newSession(sink, "s", "r", "fixture", IDs{})
	for i := 0; i < 600; i++ {
		s.Action("continue", "accepted")
	}
	if s.Failures() == 0 {
		t.Fatal("queue overflow not observable")
	}
	close(sink.block)
	s.Close()
	failed := newSession(&memoryExporter{fail: true}, "s", "r", "fixture", IDs{})
	failed.Capture("id", "capture", 1, 1, map[string]any{}, nil, time.Now())
	failed.Close()
	if failed.Status("id") != "failed" || failed.Failures() == 0 {
		t.Fatal("transport failure hidden")
	}
}
func TestConfigValidationAndPrivateHeaders(t *testing.T) {
	config, err := FromEnvironment([]string{"OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318/base/", "OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20secret"})
	if err != nil || config.Endpoint != "http://localhost:4318/base/v1/traces" || config.Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("config %v %v", config, err)
	}
	for _, env := range [][]string{{"OTEL_EXPORTER_OTLP_ENDPOINT=http://secret:password@host"}, {"OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318", "OTEL_EXPORTER_OTLP_HEADERS=Authorization=secret%0a"}, {"OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318", "OTEL_EXPORTER_OTLP_PROTOCOL=grpc"}} {
		if _, err := FromEnvironment(env); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	if config, err := FromEnvironment(nil); config != nil || err != nil {
		t.Fatal("disabled config failed")
	}
}

func TestShutdownHasDeadline(t *testing.T) {
	sink := &memoryExporter{block: make(chan struct{})}
	s := newSession(sink, "s", "r", "fixture", IDs{})
	for i := 0; i < 100; i++ {
		s.Action("continue", "accepted")
	}
	started := time.Now()
	s.Close()
	if time.Since(started) > 3500*time.Millisecond {
		t.Fatal("shutdown exceeded deadline")
	}
}
