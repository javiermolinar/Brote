package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListDoesNotMutateAndRejectsMismatchedBrokers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", root)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.RequestURI() != "/api/state?brief=1" {
			t.Error("unexpected action", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"live","status":"paused","owner":"browser"}`))
	}))
	defer server.Close()
	for _, s := range []Descriptor{{ID: "live", HTTP: server.URL}, {ID: "stale", HTTP: server.URL}, {ID: "ended", Stopped: true}, {ID: "offline", HTTP: "http://127.0.0.1:1", RPC: "127.0.0.1:2"}} {
		dir := filepath.Join(root, s.ID)
		os.MkdirAll(dir, 0700)
		if err := Write(filepath.Join(dir, "session.json"), s); err != nil {
			t.Fatal(err)
		}
	}
	list, err := List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Summary{}
	for _, s := range list {
		got[s.ID] = s
	}
	if got["live"].Status != "paused" || got["live"].Owner != "browser" || got["live"].Panel == "" {
		t.Fatal(got)
	}
	if got["stale"].Panel != "" || got["stale"].Status != "offline" {
		t.Fatal("accepted reused endpoint")
	}
	if got["ended"].Status != "ended" || !got["offline"].Recoverable {
		t.Fatal(got)
	}
}
