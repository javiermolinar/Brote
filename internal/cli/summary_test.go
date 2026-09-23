package cli

import "testing"

func TestSummaryPreservesInspectionAndRouting(t *testing.T) {
	v := obj{"owner": "agent", "binding": obj{"revision": 2}, "notification": obj{"id": "9"}, "task": obj{"id": "task", "status": "authorized", "delivery": "queued", "expires": "deadline"}, "capabilities": obj{"executionTasks": true}, "agentConnected": true, "inspectionError": "locals unavailable", "source": "large listing", "goroutines": []any{1}, "state": obj{"Pid": 123, "stopReason": "breakpoint", "currentThread": "metadata"}, "frames": []any{obj{"file": "main.go", "line": 19, "function": obj{"name": "main.process", "entry": 42}, "Locals": []any{obj{"name": "total", "value": "42"}}}}}
	got := summarizeState(v)
	for _, key := range []string{"owner", "binding", "notification", "task", "capabilities", "agentConnected", "inspectionError"} {
		if got[key] == nil {
			t.Fatalf("lost %s", key)
		}
	}
	if got["source"] != nil || got["goroutines"] != nil {
		t.Fatal("verbose fields retained")
	}
	f := got["frames"].([]any)[0].(obj)
	if f["function"] != "main.process" || f["Locals"] == nil {
		t.Fatal("lost frame values")
	}
	if v["source"] == nil {
		t.Fatal("mutated input")
	}
}

func TestSummaryPreservesServiceIdentity(t *testing.T) {
	got := summarizeState(obj{"id": "s", "run": "r", "serviceVersion": 1, "pauseEpoch": 9})
	for key, want := range (obj{"id": "s", "run": "r", "serviceVersion": 1, "pauseEpoch": 9}) {
		if got[key] != want {
			t.Fatalf("%s: got %v, want %v", key, got[key], want)
		}
	}
}
