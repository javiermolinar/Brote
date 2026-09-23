package broker

import (
	"agentdebugger/internal/definitions"
	"agentdebugger/internal/protocol"
	"testing"
	"time"
)

func installDefinitions(t *testing.T, b *broker, a *delayedAdapter, requested []any, functions bool) obj {
	t.Helper()
	done := make(chan obj, 1)
	failed := make(chan error, 1)
	go func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		v, e := b.replaceEditorDefinitions("/workspace/main.go", requested, functions)
		if e != nil {
			failed <- e
		} else {
			done <- v
		}
	}()
	request := executionRequest(t, a)
	points := asList(asObj(request["arguments"])["breakpoints"])
	results := []any{}
	for i, raw := range points {
		bp := asObj(raw)
		results = append(results, obj{"id": i + 10, "verified": num(bp["line"]) != 999, "line": num(bp["line"]) + 1})
	}
	a.send(obj{"type": "response", "request_seq": request["seq"], "command": request["command"], "success": true, "body": obj{"breakpoints": results}})
	select {
	case v := <-done:
		return v
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("installation stalled")
	}
	return nil
}
func TestDefinitionsReconcileOwnersOverlapAndRelocation(t *testing.T) {
	b, a := delayedFixture(t, "setBreakpoints")
	b.s.Project = "/workspace"
	trace := protocol.Definition{ID: "trace", Revision: 1, Owner: "cli", Kind: "tracepoint", Enabled: true, Scope: protocol.Scope{Workspace: "/workspace", Session: "s"}, Location: protocol.Location{File: "/workspace/main.go", Line: 10}, CaptureLimit: 3}
	b.s.Definitions = definitions.Store{Revision: 1, Items: []protocol.Definition{trace}}
	first := installDefinitions(t, b, a, []any{obj{"line": 10}, obj{"line": 999}}, false)
	if len(asList(first["breakpoints"])) != 2 {
		t.Fatal("missing editor points")
	}
	var editorID string
	for _, d := range b.s.Definitions.Items {
		if d.Owner == "editor" && d.Location.Line == 10 {
			editorID = d.ID
		}
	}
	if len(b.resolutions) != 3 || b.resolutions[0].AdapterID != b.resolutions[1].AdapterID {
		t.Fatalf("overlap not shared: %#v", b.resolutions)
	}
	if b.resolutions[0].Requested.Line != 10 || b.resolutions[0].Resolved.Line != 11 || b.resolutions[2].Verified {
		t.Fatal("requested/resolved verification lost")
	}
	installDefinitions(t, b, a, []any{obj{"line": 10}}, false)
	if len(b.s.Definitions.Items) != 2 || b.s.Definitions.Items[1].ID != editorID {
		t.Fatal("replay changed stable identity or erased CLI tracepoint")
	}
	installDefinitions(t, b, a, nil, false)
	if len(b.s.Definitions.Items) != 1 || b.s.Definitions.Items[0].ID != "trace" {
		t.Fatal("editor remove erased CLI definition")
	}
	if b.resolutions[0].Requested.Line != 10 {
		t.Fatal("relocation changed requested location")
	}
}

func TestFunctionDefinitionsAndDisabledPoints(t *testing.T) {
	b, a := delayedFixture(t, "setFunctionBreakpoints")
	b.s.Project = "/workspace"
	b.s.Definitions = definitions.Store{Items: []protocol.Definition{{ID: "disabled", Revision: 1, Owner: "cli", Kind: "breakpoint", Scope: protocol.Scope{Workspace: "/workspace", Session: "s"}, Location: protocol.Location{Function: "main.disabled"}}}}
	installDefinitions(t, b, a, []any{obj{"name": "main.work"}}, true)
	if len(b.s.Definitions.Items) != 2 || len(b.resolutions) != 2 {
		t.Fatal("function definitions missing")
	}
	if b.resolutions[0].Verified || b.resolutions[0].AdapterID != 0 || b.resolutions[0].Message != "disabled" {
		t.Fatal("disabled definition installed")
	}
	if b.s.Definitions.Items[1].Location.Function != "main.work" {
		t.Fatal("function expression lost")
	}
}
