package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestRecorderOverflowPersistsAcrossRecordReload(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	e := testEngine(t, func(context.Context, ptrace.Traces) error { return nil })
	server := httptest.NewServer(nil)
	defer server.Close()
	server.Config.Handler = e.handler(server.URL)
	r := &Recorder{events: make(chan Event, 2), done: make(chan struct{}), session: "overflow"}
	r.Event(Event{Kind: "start"})
	r.Event(Event{Kind: "record", Command: "retained"})
	r.Event(Event{Kind: "record", Command: "dropped"})
	go r.runEvents(server.URL, nil)
	r.Close()
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.captures, "overflow")
	c, err := e.load("overflow")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Closed || !c.Incomplete || c.Local[c.Program] != failed || c.Local[c.Debugger] != failed {
		t.Fatalf("persisted capture hides producer loss: %+v", c.Record)
	}
}

func TestRecorderCloseWaitsForColdStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &Recorder{events: make(chan Event, 2), done: make(chan struct{}), session: "startup"}
		go func() {
			time.Sleep(20 * time.Second)
			for range r.events {
			}
			close(r.done)
		}()
		r.Close()
		select {
		case <-r.done:
		default:
			t.Fatal("close returned before cold startup could drain observations")
		}
	})
}

func TestRequestPreservesBoundedCoreError(t *testing.T) {
	for _, body := range []string{`{"error":"trace export is still flushing to local blocks, or is incomplete; try again"}`, `invalid`, `{"error":"` + strings.Repeat("x", 4096) + `"}`} {
		t.Run(body[:7], func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(body))
			}))
			defer s.Close()
			err := Request(context.Background(), s.URL, "traces", nil, nil)
			if err == nil {
				t.Fatal("failure accepted")
			}
			if strings.Contains(body, "flushing") {
				if !strings.Contains(err.Error(), "flushing") {
					t.Fatal(err)
				}
			} else if err.Error() != "core tracing request failed" {
				t.Fatal(err)
			}
		})
	}
}
