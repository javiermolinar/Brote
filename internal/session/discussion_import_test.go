package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeImportLosslessAndRepeatable(t *testing.T) {
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	raw, _ := json.Marshal(map[string]any{"id": "native-id", "file": "a.go", "line": 3, "question": "latest", "answer": strings.Repeat("界", 32000), "evidence": map[string]any{"session": "vscode-debug-id", "run": "new", "capturedAt": "then", "scopes": []any{map[string]any{"error": "partial"}}}, "turns": []any{map[string]any{"question": "original", "answer": "old answer", "evidence": map[string]any{"session": "vscode-debug-id", "run": "old"}}}})
	input := NativeImport{Workspace: "workspace", Discussions: []json.RawMessage{raw}, Tracepoints: json.RawMessage(`[{"id":"bp","file":"a.go","condition":"i>1"},{"id":"unmapped"}]`), TracepointServiceIDs: map[string]string{"bp": "previous-random-id"}}
	first, err := ImportNative(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ImportNative(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Discussions[0] != second.Discussions[0] || second.TracepointServiceIDs["bp"] != "previous-random-id" || second.TracepointServiceIDs["unmapped"] == "" {
		t.Fatal("identity drift", second)
	}
	d, err := ReadDiscussion(first.Discussions[0].Session)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Threads) != 1 || len(d.Threads[0].Messages) != 4 || len(d.Threads[0].Messages[3].Body) != 96000 {
		t.Fatal("lossy import", d)
	}
	if d.Threads[0].Messages[0].Evidence.ExecutionRun != "old" || d.Threads[0].Messages[2].Evidence.ExecutionRun != "new" {
		t.Fatal("mixed evidence")
	}
	if saved, err := SavedRun(d.Session); err != nil || saved["historical"] != true {
		t.Fatal("imported history unavailable", err)
	}
	if len(d.Threads[0].Legacy) == 0 {
		t.Fatal("missing original payload")
	}
}
