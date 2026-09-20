package broker

import (
	"agentdebugger/internal/session"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestHistoryInspectionDeduplicatesWithinStop(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	s := session.Descriptor{ID: "0123456789", Project: t.TempDir(), Binary: "demo", Created: "2026-09-20T10:00:00Z"}
	h, err := session.OpenHistory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	b := &broker{history: h, stopID: "stop-1"}
	v := obj{"status": "paused", "goroutine": 7, "frames": []any{obj{"file": "main.go", "line": 18, "function": obj{"name": "main.main"}, "Locals": []any{obj{"name": "attempt", "value": "3"}}}}}
	b.historyInspection(v)
	b.historyInspection(v)
	events, err := session.ReadHistory(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("repeated refresh logged %d events", len(events))
	}
	var payload obj
	if err = json.Unmarshal(events[1].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["context_id"] != "7" {
		t.Fatal(payload)
	}
	if filepath.Ext(str(payload["snapshot"])) != ".json" {
		t.Fatal("missing snapshot")
	}
	b.stopID = "stop-2"
	b.historyInspection(v)
	events, err = session.ReadHistory(s.ID)
	if err != nil || len(events) != 3 {
		t.Fatal("new stop not captured", events, err)
	}
	if b.historyError != "" {
		t.Fatal(b.historyError)
	}
}
