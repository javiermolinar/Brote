package tracing

import (
	"encoding/json"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"strings"
	"testing"
	"time"
)

func TestDAPOrdering(t *testing.T) {
	for _, first := range []bool{false, true} {
		c := newCapture(Event{Session: "native", Name: "program"})
		c.event(Event{Kind: "request", Seq: 1, Command: "next", Thread: 7})
		if first {
			c.event(Event{Kind: "response", Seq: 1, Success: true})
		}
		if out := c.event(Event{Kind: "stopped", Thread: 8}); len(out) != 0 {
			t.Fatal("completed another thread")
		}
		if !first {
			c.event(Event{Kind: "response", Seq: 1, Success: true})
		}
		out := c.event(Event{Kind: "stopped", Thread: 7})
		if len(out) != 1 || out[0].Name() != "next" {
			t.Fatal(out)
		}
	}
}
func TestStopBeforeResponseAndFailure(t *testing.T) {
	c := newCapture(Event{Session: "dap"})
	c.event(Event{Kind: "request", Seq: 1, Command: "continue", Thread: 7})
	if len(c.event(Event{Kind: "stopped", Thread: 8, AllThreads: true})) != 0 {
		t.Fatal("finished before response")
	}
	out := c.event(Event{Kind: "response", Seq: 1, Success: false})
	if len(out) != 1 || out[0].Status().Code() != ptrace.StatusCodeError {
		t.Fatal("lost failed response")
	}
}
func TestSnapshotSchemaAndClose(t *testing.T) {
	c := newCapture(Event{Session: "native", Name: "program", Adapter: "go"})
	scope := json.RawMessage(`{"name":"Locals","variables":[{"name":"total","value":"42","type":"int"}]}`)
	out := c.event(Event{Kind: "snapshot", Label: "work.result", Selections: map[string]string{"total": "total"}, Observation: &Observation{Thread: 7, CapturedAt: time.Now(), Frame: Frame{Name: "main.work"}, Stack: []Frame{{Name: "main.work"}}, Scopes: []json.RawMessage{scope}}})
	if len(out) != 1 {
		t.Fatal(out)
	}
	s := out[0]
	for key, want := range map[string]string{"program.value.total": "42", "program.capture.status": "complete", "program.span.type": "snapshot"} {
		v, _ := s.Attributes().Get(key)
		if v.Str() != want {
			t.Fatalf("%s: %v", key, v)
		}
	}
	roots := c.event(Event{Kind: "close"})
	if len(roots) != 3 || len(c.event(Event{Kind: "close"})) != 0 {
		t.Fatal("roots must close once")
	}
	run := roots[2]
	if run.Links().Len() != 1 || run.Links().At(0).TraceID() != traceID(c.Debugger) {
		t.Fatal("program-debugger link missing")
	}
	if c.batch(append(out, roots...)).SpanCount() != 4 {
		t.Fatal("lost spans")
	}
}
func TestCaptureBounds(t *testing.T) {
	c := newCapture(Event{Session: "bounded"})
	scope, _ := json.Marshal(map[string]any{"variables": []any{map[string]string{"value": strings.Repeat("x", 40000)}}})
	out := c.event(Event{Kind: "snapshot", Observation: &Observation{Thread: 1, CapturedAt: time.Now(), Scopes: []json.RawMessage{scope}}})
	v, _ := out[0].Attributes().Get("program.snapshot.json")
	if len(v.Str()) > 32768 {
		t.Fatal("unbounded snapshot")
	}
	v, _ = out[0].Attributes().Get("program.capture.status")
	if v.Str() != "partial" {
		t.Fatal("omission not reported")
	}
}

func TestBufferedEventsKeepObservationTime(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	c := newCapture(Event{Session: "buffered", At: start})
	c.event(Event{Kind: "request", Seq: 1, Command: "next", Thread: 7, At: start.Add(time.Second)})
	c.event(Event{Kind: "response", Seq: 1, Success: true, At: start.Add(2 * time.Second)})
	out := c.event(Event{Kind: "stopped", Thread: 7, At: start.Add(3 * time.Second)})
	if c.Started != start || out[0].StartTimestamp().AsTime() != start.Add(time.Second).UTC() || out[0].EndTimestamp().AsTime() != start.Add(3*time.Second).UTC() {
		t.Fatal("startup changed observed timing")
	}
}
