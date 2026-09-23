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

func TestCanonicalAttachRequiresPositivePID(t *testing.T) {
	for _, args := range [][]string{{}, {"--pid", "0"}, {"--pid", "-1"}, {"--binary", "missing", "--", "--pid", "12"}} {
		_, err := Run(append([]string{"session", "attach"}, args...))
		if err == nil || !strings.Contains(err.Error(), "--pid must be positive") {
			t.Fatal(args, err)
		}
	}
}
func TestSessionListingAndOpenDoNotAcquireControl(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", root)
	t.Setenv("AGENTDEBUGGER_DATA_DIR", data)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/state" {
			t.Error(r.Method, r.URL)
		}
		json.NewEncoder(w).Encode(obj{"id": "live", "status": "paused", "owner": "browser"})
	}))
	defer server.Close()
	for _, s := range []session.Descriptor{{ID: "live", HTTP: server.URL, Token: "secret"}, {ID: "ended", Stopped: true}} {
		dir := filepath.Join(root, s.ID)
		os.MkdirAll(dir, 0700)
		if err := session.Write(filepath.Join(dir, "session.json"), s); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"live", "saved"} {
		dir := filepath.Join(data, "projects", "p", "runs", id)
		os.MkdirAll(dir, 0700)
		if err := session.Write(filepath.Join(dir, "session.json"), session.HistoryMetadata{ID: id, Status: "ended"}); err != nil {
			t.Fatal(err)
		}
	}
	v, err := Run([]string{"session", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if list := v.([]session.Summary); len(list) != 1 || list[0].ID != "live" {
		t.Fatal(list)
	}
	v, err = Run([]string{"session", "list", "--all"})
	if err != nil {
		t.Fatal(err)
	}
	if list := v.([]session.Summary); len(list) != 3 {
		t.Fatal(list)
	}
	before := calls
	v, err = Run([]string{"session", "open", "live"})
	if err != nil || v.(obj)["panel"] != server.URL+"/#secret" || calls != before {
		t.Fatal(v, err, calls)
	}
}
func TestCanonicalLifecycleGuards(t *testing.T) {
	for _, args := range [][]string{{"session", "stop", "missing"}, {"session", "stop", "missing", "--human"}, {"session", "restart", "missing", "--new-session", "--human"}, {"session", "history"}} {
		_, err := Run(args)
		if err == nil || strings.Contains(err.Error(), "no such file") {
			t.Fatal(args, err)
		}
	}
}
