package cli

import (
	"agentdebugger/internal/session"
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

func TestCodexCommentDeliveryWhileHumanOwnsSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", dir)
	script := filepath.Join(dir, "codex")
	calls := filepath.Join(dir, "calls")
	t.Setenv("BRIDGE_TEST_LOG", calls)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$BRIDGE_TEST_LOG\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	binding := session.Binding{ID: "client", Revision: 1, Name: "Codex"}
	d := session.Discussion{Session: "0123456789", Threads: []session.CommentThread{{ID: "thread", Messages: []session.CommentMessage{{Body: "Why?"}}, Delivery: session.CommentDelivery{Question: "question", Status: "pending", Binding: &binding}}}}
	if err := session.WriteDiscussion(d); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body obj
		json.NewDecoder(r.Body).Decode(&body)
		current, _ := session.ReadDiscussion(d.Session)
		current.Threads[0].Delivery.Status = str(body["status"])
		session.WriteDiscussion(current)
		json.NewEncoder(w).Encode(obj{})
	}))
	defer server.Close()
	s := session.Descriptor{ID: d.Session, HTTP: server.URL, Owner: "browser"}
	cfg := bridgeConfig{Thread: "11111111-1111-1111-1111-111111111111", Executable: script, Binding: binding}
	if err := deliverQuestions(s, cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(calls)
	if len(before) == 0 {
		t.Fatal("question not delivered")
	}
	if err := deliverQuestions(s, cfg); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(calls)
	if string(before) != string(after) {
		t.Fatal("duplicate delivery")
	}
	d, _ = session.ReadDiscussion(d.Session)
	d.Threads[0].Delivery.Status = "sending"
	session.WriteDiscussion(d)
	if err := deliverQuestions(s, cfg); err != nil {
		t.Fatal(err)
	}
	d, _ = session.ReadDiscussion(d.Session)
	if d.Threads[0].Delivery.Status != "unknown" {
		t.Fatal(d)
	}
}
