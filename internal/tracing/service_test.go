package tracing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/grafana/tempo/v3/pkg/tempopb"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func testEngine(t *testing.T, fn func(context.Context, ptrace.Traces) error) *engine {
	t.Helper()
	push, err := consumer.NewTraces(fn)
	if err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(t.TempDir(), push, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = e.flush(ctx)
		close(e.local)
		if e.remote != nil {
			close(e.remote)
		}
		e.workers.Wait()
	})
	return e
}
func TestIndependentDestinationsAndStickyFailure(t *testing.T) {
	var remoteIDs []string
	var mu sync.Mutex
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic secret" {
			t.Error("remote header missing")
		}
		b, _ := io.ReadAll(r.Body)
		traces, err := (&ptrace.ProtoUnmarshaler{}).UnmarshalTraces(b)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		for i := 0; i < traces.ResourceSpans().Len(); i++ {
			remoteIDs = append(remoteIDs, traces.ResourceSpans().At(i).ScopeSpans().At(0).Spans().At(0).TraceID().String())
		}
		mu.Unlock()
		w.WriteHeader(401)
	}))
	defer remote.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", remote.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "Authorization=Basic%20secret")
	calls := 0
	e := testEngine(t, func(_ context.Context, traces ptrace.Traces) error {
		calls++
		if calls == 1 {
			return errors.New("disk unavailable")
		}
		return nil
	})
	record, err := e.event(Event{Session: "core", Name: "program", Kind: "start"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.event(Event{Session: "core", Kind: "record", Command: "first"})
	_ = e.flush(context.Background())
	_, _ = e.event(Event{Session: "core", Kind: "close"})
	_ = e.flush(context.Background())
	e.mu.Lock()
	got := cloneRecord(e.captures["core"].Record)
	e.mu.Unlock()
	if got.Local[record.Debugger] != failed || got.Local[record.Program] != accepted {
		t.Fatal(got.Local)
	}
	if got.Remote[record.Debugger] != failed || got.Remote[record.Program] != failed {
		t.Fatal(got.Remote)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(remoteIDs) != 3 || remoteIDs[0] != record.Debugger {
		t.Fatal(remoteIDs)
	}
	data, err := os.ReadFile(recordPath(e.dir, "core"))
	if err != nil || strings.Contains(string(data), "secret") {
		t.Fatal("credentials reached metadata", err)
	}
}
func TestCoreRecordsSurviveReloadAndResume(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	first, _ := e.event(Event{Kind: "start", Session: "broker", Name: "demo"})
	_, _ = e.event(Event{Kind: "close", Session: "broker"})
	_ = e.flush(context.Background())
	e.mu.Lock()
	delete(e.captures, "broker")
	e.mu.Unlock()
	next, err := e.event(Event{Kind: "start", Session: "broker"})
	if err != nil || next.Program != first.Program || next.Debugger != first.Debugger || next.Run == first.Run || next.Closed {
		t.Fatal(next, err)
	}
	if len(filepath.Base(recordPath(e.dir, "../../escape"))) != 69 {
		t.Fatal("unsafe metadata path")
	}
}
func TestCoreHTTPBoundary(t *testing.T) {
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	handler := e.handler("http://127.0.0.1:1234")
	for _, tc := range []struct {
		host, origin, content, body string
		code                        int
	}{{"127.0.0.1:1234", "https://evil.invalid", "application/json", `{}`, 403}, {"evil.invalid", "", "application/json", `{}`, 403}, {"127.0.0.1:1234", "", "text/plain", `{}`, 415}, {"127.0.0.1:1234", "", "application/json", `{"kind":"start","session":"native","name":"demo"}`, 200}} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/api/trace-events", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("got %d: %s", w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var record Record
			if json.Unmarshal(w.Body.Bytes(), &record) != nil || len(record.Program) != 32 {
				t.Fatal(w.Body.String())
			}
		}
	}
}

func TestQueriesUseBackendAndRejectIncompleteFragments(t *testing.T) {
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	record, _ := e.event(Event{Kind: "start", Session: "pending"})
	_, _ = e.event(Event{Kind: "record", Session: "pending", Command: "captured"})
	_ = e.flush(context.Background())
	e.queries = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mode") != "blocks" {
			t.Error("query touched mutable live data")
		}
		body, _ := (&tempopb.TraceByIDResponse{Trace: &tempopb.Trace{}}).Marshal()
		w.Write(body)
	})
	if _, err := e.query(context.Background(), record.Debugger); err == nil || !strings.Contains(err.Error(), "flushing") {
		t.Fatal("partial trace accepted", err)
	}
	e.mu.Lock()
	delete(e.captures, "pending")
	e.mu.Unlock()
	if _, err := e.query(context.Background(), record.Debugger); err == nil {
		t.Fatal("saved expected count ignored")
	}
}

func TestRestartMarksAbandonedRecordsBeforeListing(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	r, _ := e.event(Event{Kind: "start", Session: "crashed"})
	_, _ = e.event(Event{Kind: "record", Session: "crashed", Command: "last accepted"})
	_ = e.flush(context.Background())
	// Construct the replacement engine over the same metadata without a producer
	// reconnect. The abandoned root spans cannot be reconstructed after a crash.
	push, _ := consumer.NewTraces(func(context.Context, ptrace.Traces) error { return nil })
	restarted, err := newEngine(e.dir, push, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(restarted.local); restarted.workers.Wait() }()
	w := httptest.NewRecorder()
	restarted.handler("http://127.0.0.1:1234").ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:1234/api/trace-sessions", nil))
	var records []Record
	if err := json.Unmarshal(w.Body.Bytes(), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || !records[0].Closed || !records[0].Incomplete || records[0].Local[r.Debugger] != failed {
		t.Fatalf("stale accepted record: %s", w.Body.String())
	}
	resumed, err := restarted.event(Event{Kind: "heartbeat", Session: "crashed"})
	if err != nil || resumed.Closed || !resumed.Incomplete || resumed.Run == r.Run || resumed.Program != r.Program {
		t.Fatalf("bad recovery: %+v, %v", resumed, err)
	}
	continued, err := restarted.event(Event{Kind: "record", Session: "crashed", Command: "after reconnect"})
	if err != nil || continued.DebuggerSpans != records[0].DebuggerSpans+1 {
		t.Fatalf("observation after reconnect dropped: %+v, %v", continued, err)
	}
	_, _ = restarted.event(Event{Kind: "close", Session: "crashed"})
	closed, _ := restarted.event(Event{Kind: "heartbeat", Session: "crashed"})
	if !closed.Closed {
		t.Fatal("heartbeat reopened a finalized capture")
	}
	_ = restarted.flush(context.Background())
}

func TestInterruptedCaptureCanResumeOrBeExplicitlyFinalized(t *testing.T) {
	for _, finalize := range []bool{false, true} {
		t.Run(fmt.Sprint(finalize), func(t *testing.T) {
			e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
			r, _ := e.event(Event{Kind: "start", Session: "interrupted"})
			e.mu.Lock()
			e.interruptCapture(e.captures["interrupted"])
			e.mu.Unlock()
			_ = e.flush(context.Background())
			if finalize {
				_, _ = e.event(Event{Kind: "close", Session: "interrupted"})
			}
			next, err := e.event(Event{Kind: "heartbeat", Session: "interrupted"})
			if err != nil || !next.Incomplete || next.Closed != finalize {
				t.Fatalf("bad interruption recovery: %+v, %v", next, err)
			}
			if !finalize && next.Run == r.Run {
				t.Fatal("resumption reused closed root")
			}
			if finalize && next.Interrupted {
				t.Fatal("explicit close retained resumable marker")
			}
		})
	}
}
