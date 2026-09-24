package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"sync"
	"time"
)

type IDs struct {
	Debugger     string `json:"debuggerTraceId"`
	Program      string `json:"programTraceId"`
	DebuggerRoot string `json:"debuggerRootId"`
	ProgramRoot  string `json:"programRootId"`
}
type ids struct {
	t trace.TraceID
	s trace.SpanID
}

func (i ids) NewIDs(context.Context) (trace.TraceID, trace.SpanID) { return i.t, i.s }
func (i ids) NewSpanID(context.Context, trace.TraceID) trace.SpanID {
	var s trace.SpanID
	_, _ = rand.Read(s[:])
	return s
}
func makeIDs(t, s string) ids {
	tid, _ := trace.TraceIDFromHex(t)
	sid, _ := trace.SpanIDFromHex(s)
	if !tid.IsValid() {
		_, _ = rand.Read(tid[:])
	}
	if !sid.IsValid() {
		_, _ = rand.Read(sid[:])
	}
	return ids{tid, sid}
}

type Session struct {
	IDs                     IDs
	q                       *queue
	debugger, program       trace.Tracer
	root, run               trace.Span
	rootContext, runContext context.Context
	session, runID          string
	captureSpans            map[string]string
	threads                 map[int]trace.Span
	mu                      sync.Mutex
	closed                  bool
}

func New(config *Config, session, runID, name string, saved IDs) (*Session, error) {
	if config == nil {
		return nil, nil
	}
	exporter, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(config.Endpoint), otlptracehttp.WithHeaders(config.Headers), otlptracehttp.WithTimeout(2*time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
	if err != nil {
		return nil, fmt.Errorf("OTLP exporter initialization failed")
	}
	return newSession(exporter, session, runID, name, saved), nil
}
func NewWithExporter(exporter sdk.SpanExporter, session, runID, name string, saved IDs) *Session {
	return newSession(exporter, session, runID, name, saved)
}
func newSession(exporter sdk.SpanExporter, session, runID, name string, saved IDs) *Session {
	q := newQueue(exporter)
	d, p := makeIDs(saved.Debugger, saved.DebuggerRoot), makeIDs(saved.Program, saved.ProgramRoot)
	provider := func(service string, ids ids) *sdk.TracerProvider {
		return sdk.NewTracerProvider(sdk.WithIDGenerator(ids), sdk.WithSpanProcessor(q), sdk.WithResource(resource.NewSchemaless(attribute.String("service.name", service), attribute.String("debugger.adapter.type", "go"))), sdk.WithRawSpanLimits(sdk.SpanLimits{AttributeCountLimit: 128, AttributeValueLengthLimit: 65536, EventCountLimit: 8, LinkCountLimit: 4, AttributePerEventCountLimit: 16, AttributePerLinkCountLimit: 8}))
	}
	s := &Session{q: q, session: session, runID: runID, threads: map[int]trace.Span{}, captureSpans: map[string]string{}, debugger: provider("brote", d).Tracer("brote.debugger"), program: provider(name, p).Tracer("brote.program")}
	s.rootContext, s.root = s.debugger.Start(context.Background(), "debugger.session", trace.WithAttributes(s.common()...))
	s.runContext, s.run = s.program.Start(context.Background(), "run "+name, trace.WithAttributes(append(s.common(), attribute.String("program.span.type", "run"))...), trace.WithLinks(trace.Link{SpanContext: s.root.SpanContext()}))
	s.IDs = IDs{Debugger: d.t.String(), DebuggerRoot: d.s.String(), Program: p.t.String(), ProgramRoot: p.s.String()}
	return s
}
func (s *Session) common() []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("debugger.session.id", s.session), attribute.String("program.run.id", s.runID), attribute.Int("program.schema.version", 2)}
}
func (s *Session) Action(command, outcome string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	now := time.Now()
	_, span := s.debugger.Start(s.rootContext, command, trace.WithTimestamp(now), trace.WithAttributes(attribute.String("debugger.session.id", s.session), attribute.String("program.run.id", s.runID), attribute.String("debugger.command", command), attribute.String("debugger.outcome", outcome)))
	span.End(trace.WithTimestamp(now))
}
func (s *Session) Capture(id, name string, sequence uint64, gid int, snapshot, values map[string]any, created time.Time) string {
	if s == nil {
		return "disabled"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "failed"
	}
	payload, err := json.Marshal(snapshot)
	if err != nil || len(payload) > 32768 {
		return "failed"
	}
	parent := s.threads[gid]
	if parent == nil {
		if len(s.threads) >= 256 {
			return "failed"
		}
		_, parent = s.program.Start(s.runContext, fmt.Sprintf("goroutine %d observed", gid), trace.WithTimestamp(created), trace.WithAttributes(append(s.common(), attribute.String("program.span.type", "thread"), attribute.Int("program.thread.id", gid), attribute.String("program.observation.boundary", "first-observed"))...))
		s.threads[gid] = parent
	}
	attrs := append(s.common(), attribute.String("program.span.type", "snapshot"), attribute.Int("program.thread.id", gid), attribute.Int64("program.snapshot.sequence", int64(sequence)), attribute.String("program.capture.id", id), attribute.String("program.capture.name", name), attribute.String("program.capture.status", "complete"), attribute.String("program.snapshot.json", string(payload)))
	for alias, raw := range values {
		value, _ := raw.(map[string]any)
		status := "available"
		if value["error"] != nil {
			status = "failed"
		} else if value["truncated"] == true {
			status = "truncated"
		} else if value["children"] != nil || value["kind"] == 23 {
			status = "non_scalar"
		}
		attrs = append(attrs, attribute.String("program.value_status."+alias, status))
		if kind, ok := value["type"].(string); ok {
			attrs = append(attrs, attribute.String("program.value_type."+alias, kind))
		}
		if status == "available" {
			if text, ok := value["value"].(string); ok {
				attrs = append(attrs, attribute.String("program.value."+alias, text))
			}
		}
	}
	if name == "" {
		name = "capture"
	}
	_, span := s.program.Start(trace.ContextWithSpan(s.runContext, parent), name, trace.WithTimestamp(created), trace.WithAttributes(attrs...))
	s.captureSpans[id] = span.SpanContext().SpanID().String()
	span.End(trace.WithTimestamp(created))
	return s.q.Status(id)
}
func (s *Session) Status(id string) string {
	if s == nil {
		return "disabled"
	}
	return s.q.Status(id)
}
func (s *Session) Failures() int {
	if s == nil {
		return 0
	}
	return s.q.Failures()
}
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for _, thread := range s.threads {
		thread.End()
	}
	s.run.End()
	s.root.End()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.q.Shutdown(ctx)
}

// CaptureSpanID returns the actual span identity assigned to a captured observation.
func (s *Session) CaptureSpanID(id string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.captureSpans[id]
}
