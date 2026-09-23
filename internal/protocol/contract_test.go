package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRequestIsolation(t *testing.T) {
	current := Identity{Session: "s", Run: "r", Generation: 7, PauseEpoch: 3}
	base := Request{Version: Version, Identity: current, Client: "cli", CommandID: "c", Operation: "pause"}
	for _, tc := range []struct {
		name   string
		change func(*Request)
		human  bool
		code   string
	}{
		{"current", func(*Request) {}, false, ""},
		{"legacy version", func(r *Request) { r.Version = 0 }, false, "incompatible_protocol"},
		{"future version", func(r *Request) { r.Version = 2 }, false, "incompatible_protocol"},
		{"wrong session human", func(r *Request) { r.Session = "other" }, true, "identity_mismatch"},
		{"wrong run human", func(r *Request) { r.Run = "old" }, true, "identity_mismatch"},
		{"stale agent", func(r *Request) { r.Generation-- }, false, "stale_revision"},
		{"stale human pause", func(r *Request) { r.Generation-- }, true, ""},
		{"stale human continue", func(r *Request) { r.Generation--; r.Operation = "continue" }, true, "stale_revision"},
		{"raw dap", func(r *Request) { r.Operation = "customRequest" }, false, "unsupported_operation"},
		{"missing client", func(r *Request) { r.Client = "" }, false, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.change(&r)
			err := r.Check(current, map[string]bool{"pause": true, "continue": true}, tc.human)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var problem *Error
			if !errors.As(err, &problem) || problem.Code != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
			data, e := json.Marshal(problem)
			if e != nil || !json.Valid(data) {
				t.Fatal("error is not JSON")
			}
		})
	}
}

func TestCapabilityIntersection(t *testing.T) {
	adapter := map[string]any{"supportsFunctionBreakpoints": true, "supportsConditionalBreakpoints": false, "supportsStepBack": true, "supportsSetVariable": true, "supportsRestartRequest": true, "futureCapability": true}
	got := DAPCapabilities(adapter)
	if len(got) != 3 || got["supportsFunctionBreakpoints"] != true {
		t.Fatalf("unexpected capabilities: %v", got)
	}
	got["supportsFunctionBreakpoints"] = false
	if adapter["supportsFunctionBreakpoints"] != true {
		t.Fatal("modified backend capabilities")
	}
	for _, command := range []string{"setVariable", "setExpression", "stepBack", "readMemory", "custom"} {
		if DAPRequestSupported(command) {
			t.Fatalf("exposed deferred operation %s", command)
		}
	}
}
