package cli

import (
	"debug-handover/internal/session"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCodexBridgeDeliveryAndStaleEvent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "codex")
	calls := filepath.Join(dir, "calls")
	t.Setenv("BRIDGE_TEST_LOG", calls)
	if e := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$BRIDGE_TEST_LOG\"\n"), 0755); e != nil {
		t.Fatal(e)
	}
	binding := session.Binding{ID: "client", Revision: 1, Name: "Codex"}
	status := "pending"
	owner := "agent"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(obj{"owner": owner, "generation": 1, "binding": binding, "notification": obj{"id": "1", "status": status}})
			return
		}
		var body obj
		json.NewDecoder(r.Body).Decode(&body)
		status = str(body["status"])
		json.NewEncoder(w).Encode(obj{"status": status})
	}))
	defer server.Close()
	s := session.Descriptor{ID: "test", HTTP: server.URL}
	cfg := bridgeConfig{Thread: "11111111-1111-1111-1111-111111111111", Executable: script, Binding: binding}
	event := session.Event{ID: 1, Kind: "control_returned", Owner: "agent", Binding: &binding}
	if e := deliverCodex(s, cfg, event); e != nil {
		t.Fatal(e)
	}
	if status != "queued" {
		t.Fatal(status)
	}
	before, _ := os.ReadFile(calls)
	// Duplicate and stale events do not send another message.
	if e := deliverCodex(s, cfg, event); e != nil {
		t.Fatal(e)
	}
	owner = "browser"
	status = "pending"
	if e := deliverCodex(s, cfg, event); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(calls)
	if string(before) != string(after) {
		t.Fatal("duplicate delivery")
	}
	owner = "agent"
	status = "sending"
	if e := deliverCodex(s, cfg, event); e != nil {
		t.Fatal(e)
	}
	if status != "unknown" {
		t.Fatal(status)
	}
	if len(before) == 0 {
		t.Fatal("nothing delivered " + strconv.Itoa(len(before)))
	}
}
