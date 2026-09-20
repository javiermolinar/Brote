package cli

import "testing"

func TestSummaryPreservesInspectionAndRouting(t *testing.T) {
	v := obj{"owner": "agent", "binding": obj{"revision": 2}, "notification": obj{"id": "9"}, "inspectionError": "locals unavailable", "source": "large listing", "goroutines": []any{1}, "state": obj{"Pid": 123, "stopReason": "breakpoint", "currentThread": "metadata"}, "frames": []any{obj{"file": "main.go", "line": 19, "function": obj{"name": "main.process", "entry": 42}, "Locals": []any{obj{"name": "total", "value": "42"}}}}}
	got := summarizeState(v)
	for _, key := range []string{"owner", "binding", "notification", "inspectionError"} {
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
