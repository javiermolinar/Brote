package cli

import (
	"agentdebugger/internal/session"
	"agentdebugger/internal/tracing"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSavedTraceQuery(t *testing.T) {
	data := t.TempDir()
	t.Setenv("AGENTDEBUGGER_DATA_DIR", data)
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	const traceID = "0123456789abcdef0123456789abcdef"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(obj{"service": "brote-tracing", "version": 1})
		case "/api/trace-sessions":
			json.NewEncoder(w).Encode([]tracing.Record{{Session: "chosen", Name: "other"}, {Session: "other", Name: "chosen"}})
		case "/api/traces":
			if r.URL.Query().Get("id") != traceID {
				t.Error(r.URL)
			}
			json.NewEncoder(w).Encode(obj{"traceID": traceID})
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := filepath.Join(data, "tracing")
	os.MkdirAll(dir, 0700)
	if err := session.Write(filepath.Join(dir, "endpoint.json"), server.URL); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args   []string
		length int
	}{{nil, 2}, {[]string{"--session", "chosen"}, 1}, {[]string{"--session", "missing"}, 0}} {
		value, err := Run(append([]string{"query", "traces"}, tc.args...))
		if err != nil {
			t.Fatal(err)
		}
		records := value.([]tracing.Record)
		if len(records) != tc.length {
			t.Fatal(records)
		}
		if tc.length == 1 && records[0].Session != "chosen" {
			t.Fatal(records)
		}
	}
	if _, err := Run([]string{"query", "traces", traceID}); err != nil {
		t.Fatal(err)
	}
	before := calls
	for _, args := range [][]string{{"bad"}, {"--session", ""}, {"--session", "chosen", traceID}, {traceID, "--session", "chosen"}, {traceID, traceID}} {
		if _, err := Run(append([]string{"query", "traces"}, args...)); err == nil {
			t.Fatal(args)
		}
	}
	if calls != before {
		t.Fatal("invalid query accessed storage")
	}
}

func TestTraceSessionProvenance(t *testing.T) {
	for _, tc := range []struct {
		record tracing.Record
		match  bool
	}{
		{tracing.Record{Session: "chosen", Adapter: "delve"}, true},
		{tracing.Record{Session: "chosen:run1", Adapter: "brote"}, true},
		{tracing.Record{Session: "chosen:run2", Adapter: "brote"}, true},
		{tracing.Record{Session: "chosen:run1", Adapter: "native"}, false},
		{tracing.Record{Session: "chosen-other:run1", Adapter: "brote"}, false},
		{tracing.Record{Session: "other", Name: "chosen", Adapter: "brote"}, false},
		{tracing.Record{Session: "chosen:", Adapter: "brote"}, false},
	} {
		if got := traceRecordMatchesSession(tc.record, "chosen"); got != tc.match {
			t.Fatal(tc.record, got)
		}
	}
}
