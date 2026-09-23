package broker

import (
	"agentdebugger/internal/definitions"
	"agentdebugger/internal/protocol"
	"testing"
)

func TestTracepointAttribution(t *testing.T) {
	trace := protocol.Definition{ID: "trace", Kind: "tracepoint", Enabled: true}
	resolution := definitionResolution{DefinitionID: "trace", Run: "run", AdapterID: 7, Verified: true, Requested: protocol.Location{File: "/main.go", Line: 10}, Resolved: protocol.Location{File: "/main.go", Line: 11}}
	body := obj{"reason": "breakpoint", "threadId": 1, "allThreadsStopped": true, "hitBreakpointIds": []any{7}}
	tests := []struct {
		name     string
		change   func(obj, *definitions.Store, *[]definitionResolution)
		eligible bool
	}{
		{"verified relocation", func(obj, *definitions.Store, *[]definitionResolution) {}, true},
		{"missing IDs", func(v obj, _ *definitions.Store, _ *[]definitionResolution) { delete(v, "hitBreakpointIds") }, false},
		{"unknown IDs", func(v obj, _ *definitions.Store, _ *[]definitionResolution) { v["hitBreakpointIds"] = []any{8} }, false},
		{"panic", func(v obj, _ *definitions.Store, _ *[]definitionResolution) { v["reason"] = "exception" }, false},
		{"manual pause", func(v obj, _ *definitions.Store, _ *[]definitionResolution) { v["reason"] = "pause" }, false},
		{"step", func(v obj, _ *definitions.Store, _ *[]definitionResolution) { v["reason"] = "step" }, false},
		{"ambiguous threads", func(v obj, _ *definitions.Store, _ *[]definitionResolution) { delete(v, "allThreadsStopped") }, false},
		{"no hitting thread", func(v obj, _ *definitions.Store, _ *[]definitionResolution) { v["threadId"] = 0 }, false},
		{"obsolete run", func(_ obj, _ *definitions.Store, r *[]definitionResolution) { (*r)[0].Run = "old" }, false},
		{"unverified", func(_ obj, _ *definitions.Store, r *[]definitionResolution) { (*r)[0].Verified = false }, false},
		{"changed config", func(_ obj, s *definitions.Store, _ *[]definitionResolution) { s.Items = nil }, false},
		{"mixed ownership", func(_ obj, s *definitions.Store, r *[]definitionResolution) {
			d := trace
			d.ID = "ordinary"
			d.Kind = "breakpoint"
			s.Items = append(s.Items, d)
			point := resolution
			point.DefinitionID = d.ID
			*r = append(*r, point)
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := pick(body, "reason", "threadId", "allThreadsStopped", "hitBreakpointIds")
			s := definitions.Store{Items: []protocol.Definition{trace}}
			r := []definitionResolution{resolution}
			tc.change(v, &s, &r)
			result := attributeStop(v, "run", s, r, nil)
			if result.Eligible != tc.eligible {
				t.Fatalf("%#v", result)
			}
		})
	}
	legacy := []any{obj{"id": 8, "client": "legacy-cli", "file": "/main.go", "line": 11}}
	if attributeStop(body, "run", definitions.Store{Items: []protocol.Definition{trace}}, []definitionResolution{resolution}, legacy).Eligible {
		t.Fatal("legacy overlapping ordinary breakpoint ignored")
	}
}
