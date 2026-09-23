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
)

func TestWorkspaceGatewayLandingAndOriginBoundary(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	h := workspaceHandler("http://127.0.0.1:9876")
	for _, path := range []string{"/", "/app.js", "/api/workspace", "/health"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:9876"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:9876/api/workspace", nil)
	r.Header.Set("Origin", "http://other.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:9876/api/state?session=../escape", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	var result map[string]any
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result["error"] == nil {
		t.Fatal(w.Body.String())
	}
}

func TestWorkspaceCannotTerminateAuthenticatedSession(t *testing.T) {
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	dir := filepath.Join(session.Root(), "secured")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := session.Write(filepath.Join(dir, "session.json"), session.Descriptor{ID: "secured", ServiceVersion: 1, RunID: "r", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:9876/api/sessions/stop", strings.NewReader(`{"id":"secured","confirmed":true}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	workspaceHandler("http://127.0.0.1:9876").ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("gateway bypassed admission: %d %s", w.Code, w.Body.String())
	}
}
