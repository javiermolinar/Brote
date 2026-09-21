package cli

import (
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTaskDeliveryReconcilesWithoutReplay(t *testing.T) {
	binding := session.Binding{ID: "codex:test", Revision: 2, Name: "Codex"}
	task := session.ExecutionTask{ID: "task", Status: "authorized", Delivery: "pending", Binding: &binding, Instruction: "Find the retry"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var a obj
			json.NewDecoder(r.Body).Decode(&a)
			task.Delivery = str(a["status"])
		}
		json.NewEncoder(w).Encode(obj{"generation": 1, "task": task})
	}))
	defer server.Close()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "codex")
	t.Setenv("TASK_QUEUE_LOG", log)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TASK_QUEUE_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := bridgeConfig{Thread: "thread", Executable: script, Binding: binding}
	s := session.Descriptor{HTTP: server.URL}
	for i := 0; i < 2; i++ {
		if err := deliverTask(s, cfg); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(log)
	if task.Delivery != "queued" || strings.Count(string(data), "--thread") != 1 || !strings.Contains(string(data), "task-execute") {
		t.Fatalf("%+v %s", task, data)
	}
	task.Delivery = "sending"
	if err := deliverTask(s, cfg); err != nil {
		t.Fatal(err)
	}
	if task.Delivery != "unknown" {
		t.Fatal(task)
	}
	task.Delivery = "pending"
	task.Status = "cancelled"
	if err := deliverTask(s, cfg); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(log)
	if string(after) != string(data) {
		t.Fatal("replayed cancelled or ambiguous task")
	}
}

func TestTaskExecutionRenewsAndCancelsOnTimeout(t *testing.T) {
	var mu sync.Mutex
	actions := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" {
			var a obj
			json.NewDecoder(r.Body).Decode(&a)
			if a["binding"] != "pi:test" || a["task"] != "task" {
				t.Error(a)
			}
			actions = append(actions, str(a["action"]))
		}
		json.NewEncoder(w).Encode(obj{"generation": 1, "status": "running", "task": obj{"id": "task", "status": "active"}})
	}))
	defer server.Close()
	_, err := executeTask(context.Background(), session.Descriptor{HTTP: server.URL}, "pi:test", "task", "continue", 100*time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(actions) < 4 || actions[0] != "task-heartbeat" || actions[1] != "continue" || actions[len(actions)-1] != "task-cancel" {
		t.Fatal(actions)
	}
}

func TestTaskExecutionDoesNotDispatchAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; json.NewEncoder(w).Encode(obj{}) }))
	defer server.Close()
	if _, err := executeTask(ctx, session.Descriptor{HTTP: server.URL}, "binding", "task", "next", time.Second, time.Second); err == nil {
		t.Fatal("expected cancellation")
	}
	if calls != 0 {
		t.Fatal("cancelled operation contacted broker")
	}
}
