package cli

import (
	"agentdebugger/internal/session"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSavedAnnotationRoutingAndOwner(t *testing.T) {
	data := t.TempDir()
	t.Setenv("AGENTDEBUGGER_DATA_DIR", data)
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	h, err := session.OpenHistory(session.Descriptor{ID: "abcdef1234", Project: t.TempDir(), Created: time.Now().Format(time.RFC3339Nano)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/health" {
			json.NewEncoder(w).Encode(map[string]any{"service": "brote-tracing", "version": 1})
			return
		}
		calls++
		if r.Method == "POST" {
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if in["session"] != "abcdef1234:r" {
				t.Error(in)
			}
			json.NewEncoder(w).Encode(in)
		} else {
			json.NewEncoder(w).Encode([]any{})
		}
	}))
	defer backend.Close()
	os.MkdirAll(filepath.Join(data, "tracing"), 0700)
	session.Write(filepath.Join(data, "tracing", "endpoint.json"), backend.URL)
	handler := workspaceHandler("http://127.0.0.1:9876")
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{{"GET", "/api/annotations?history=abcdef1234&traceSession=abcdef1234:r", "", 200}, {"POST", "/api/annotations?history=abcdef1234", `{"session":"abcdef1234:r","id":"n","body":"note","author":"human","revision":1,"targets":[]}`, 200}, {"GET", "/api/annotations?history=abcdef1234&traceSession=other:r", "", 409}, {"POST", "/api/annotation-evidence?history=abcdef1234", `{}`, 405}, {"GET", "/api/annotation-evidence?history=missing", "", 404}} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1:9876"+tc.path, strings.NewReader(tc.body))
		if tc.method == "POST" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if calls != 2 {
		t.Fatal("invalid requests reached service", calls)
	}
}
