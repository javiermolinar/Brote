package definitions

import (
	"agentdebugger/internal/protocol"
	"encoding/json"
	"testing"
)

func sample() protocol.Definition {
	return protocol.Definition{ID: "stable", Owner: "cli", Kind: "tracepoint", Enabled: true, Scope: protocol.Scope{Workspace: "/workspace", Session: "session"}, Location: protocol.Location{File: "/workspace/main.go", Line: 10}, CaptureLimit: 5, Values: map[string]string{"value": "value"}}
}
func TestDefinitionRevisionsAndOwnership(t *testing.T) {
	d := sample()
	if err := Validate(d, "session", "run", "/workspace"); err != nil {
		t.Fatal(err)
	}
	s, err := (Store{}).Put(d, 0)
	if err != nil {
		t.Fatal(err)
	}
	d.Name = "new"
	for _, owner := range []string{"cli", "editor"} {
		d.Owner = owner
		if _, err = s.Put(d, 0); err == nil {
			t.Fatal("stale or foreign edit accepted")
		}
	}
	d.Owner = "cli"
	next, err := s.Put(d, 1)
	if err != nil || next.Items[0].Revision != 2 || s.Items[0].Name != "" {
		t.Fatalf("revision update %v %v", next, err)
	}
	next.Items[0].Values["value"] = "changed"
	if s.Items[0].Values["value"] != "value" {
		t.Fatal("store copy aliases values")
	}
	if _, err = next.Delete(d.ID, "editor", 2); err == nil {
		t.Fatal("foreign deletion accepted")
	}
	if next, err = next.Delete(d.ID, "cli", 2); err != nil || len(next.Items) != 0 {
		t.Fatal("delete failed")
	}
}
func TestPersistenceAndRunScope(t *testing.T) {
	d := sample()
	s, _ := (Store{}).Put(d, 0)
	d.ID = "run-only"
	d.Scope.Run = "old"
	s, _ = s.Put(d, 0)
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var loaded Store
	if err = json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	restarted := loaded.ForRun("new")
	if len(restarted.Items) != 1 || restarted.Items[0].ID != "stable" || restarted.Items[0].Revision != 1 {
		t.Fatalf("restart lost identity: %#v", restarted)
	}
}
func TestRejectFunctionTracepointAndWrongScope(t *testing.T) {
	d := sample()
	d.Location = protocol.Location{Function: "main.work"}
	if Validate(d, "session", "run", "/workspace") == nil {
		t.Fatal("function tracing accepted")
	}
	d = sample()
	d.Scope.Run = "old"
	if Validate(d, "session", "new", "/workspace") == nil {
		t.Fatal("obsolete scope accepted")
	}
}
