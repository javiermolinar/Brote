package broker

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceDefinitionPersistsAndRejectsStaleEdits(t *testing.T) {
	b := &broker{s: session.Descriptor{ID: "s", RunID: "r", Project: "/workspace", ServiceVersion: 1, Dir: t.TempDir()}, generation: 1}
	request := serviceRequest{Request: protocol.Request{Version: 1, Identity: b.identity(), Client: "cli", CommandID: "create", Operation: "definition.put"}, Definition: &protocol.Definition{Kind: "tracepoint", Enabled: true, Scope: protocol.Scope{Workspace: "/workspace", Session: "s"}, Location: protocol.Location{File: "/workspace/main.go", Line: 10}, CaptureLimit: 3}}
	result, err := b.service(request)
	if err != nil {
		t.Fatal(err)
	}
	if result["version"] != 1 {
		t.Fatal("missing version")
	}
	data, err := os.ReadFile(filepath.Join(b.s.Dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved session.Descriptor
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Definitions.Items) != 1 || saved.Definitions.Items[0].ID == "" {
		t.Fatal("definition not persisted")
	}
	request.Definition.ID = saved.Definitions.Items[0].ID
	request.Identity = b.identity()
	if _, err = b.service(request); err == nil {
		t.Fatal("stale revision accepted")
	}
	request.Revision = 1
	request.Definition.Values = map[string]string{"unsafe": "work()"}
	if _, err = b.service(request); err == nil {
		t.Fatal("function call capture accepted")
	}
}
