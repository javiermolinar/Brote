package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEndUsesFreshIdentityOwnerAndGeneration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", root)
	identity := "abc"
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"id": identity, "owner": "vscode", "generation": 42})
			return
		}
		posts++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["action"] != "stop" || body["actor"] != "vscode" || body["generation"] != float64(42) {
			t.Error(body)
		}
		w.Write([]byte(`{"status":"ended"}`))
	}))
	defer server.Close()
	os.MkdirAll(filepath.Join(root, "abc"), 0700)
	Write(filepath.Join(root, "abc", "session.json"), Descriptor{ID: "abc", HTTP: server.URL})
	if _, err := End(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
	identity = "other"
	if _, err := End(context.Background(), "abc"); err == nil {
		t.Fatal("accepted stale identity")
	}
	if posts != 1 {
		t.Fatal("unexpected stop count", posts)
	}
}
