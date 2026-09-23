package broker

import (
	"agentdebugger/internal/definitions"
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
)

type definitionResolution struct {
	DefinitionID string            `json:"definitionId"`
	Run          string            `json:"run"`
	AdapterID    int               `json:"adapterId,omitempty"`
	Verified     bool              `json:"verified"`
	Requested    protocol.Location `json:"requested"`
	Resolved     protocol.Location `json:"resolved"`
	Message      string            `json:"message,omitempty"`
}

// All definitions share one backend owner. Identical physical requests are
// deduplicated, while each logical owner retains its own stable definition.
func (b *broker) reconcileDefinitions() error {
	if b.backend == nil {
		return nil
	}
	files := map[string]bool{}
	existing, _ := b.backend.Call("ListBreakpoints", obj{})
	for _, raw := range asList(existing["Breakpoints"]) {
		bp := asObj(raw)
		if str(bp["client"]) == "brote-definitions" {
			if str(bp["kind"]) == "function" {
				files[""] = true
			} else {
				files[str(bp["file"])] = true
			}
		}
	}
	for _, r := range b.resolutions {
		files[r.Requested.File] = true
	}
	for _, d := range b.s.Definitions.Items {
		files[d.Location.File] = true
	}
	// Clear attribution before touching the adapter; partial failure must never
	// leave an apparently verified map from the previous installation.
	b.resolutions = nil
	keys := []string{}
	for file := range files {
		keys = append(keys, file)
	}
	sort.Strings(keys)
	for _, file := range keys {
		requests := []any{}
		positions := map[string]int{}
		indexes := map[string]int{}
		for _, d := range b.s.Definitions.Items {
			if d.Location.File != file {
				continue
			}
			if !d.Enabled || (d.Scope.Run != "" && d.Scope.Run != b.s.RunID) {
				b.resolutions = append(b.resolutions, definitionResolution{DefinitionID: d.ID, Run: b.s.RunID, Requested: d.Location, Message: "disabled"})
				continue
			}
			request := obj{"line": d.Location.Line, "name": d.Location.Function, "condition": d.Condition, "hitCondition": d.HitCondition}
			encoded, _ := json.Marshal(request)
			key := string(encoded)
			index, ok := positions[key]
			if !ok {
				index = len(requests)
				positions[key] = index
				requests = append(requests, request)
			}
			indexes[d.ID] = index
		}
		response, err := b.backend.ReplaceBreakpoints("brote-definitions", file, requests, file == "")
		if err != nil {
			b.resolutions = nil
			return err
		}
		results := asList(response["breakpoints"])
		for _, d := range b.s.Definitions.Items {
			index, ok := indexes[d.ID]
			if !ok {
				continue
			}
			if index >= len(results) {
				b.resolutions = nil
				return fmt.Errorf("incomplete breakpoint installation")
			}
			item := asObj(results[index])
			location := d.Location
			if path := str(asObj(item["source"])["path"]); path != "" {
				location.File = path
			}
			if line := num(item["line"]); line > 0 {
				location.Line = line
			}
			b.resolutions = append(b.resolutions, definitionResolution{DefinitionID: d.ID, Run: b.s.RunID, AdapterID: num(item["id"]), Verified: truth(item["verified"]), Requested: d.Location, Resolved: location, Message: str(item["message"])})
		}
	}
	return nil
}
func (b *broker) replaceEditorDefinitions(file string, requested []any, functions bool) (obj, error) {
	if len(requested) > definitions.Limit {
		return nil, fmt.Errorf("too many breakpoints")
	}
	if b.moving || b.interrupting || truth(b.backend.State()["Running"]) {
		return nil, fmt.Errorf("pause before changing breakpoint definitions")
	}
	next := b.s.Definitions.Copy()
	old := []protocol.Definition{}
	keep := []protocol.Definition{}
	for _, d := range next.Items {
		same := d.Owner == "editor" && d.Kind == "breakpoint" && ((functions && d.Location.Function != "") || (!functions && d.Location.Function == "" && d.Location.File == file))
		if same {
			old = append(old, d)
		} else {
			keep = append(keep, d)
		}
	}
	next.Items = keep
	ids := []string{}
	for _, raw := range requested {
		item := asObj(raw)
		if str(item["logMessage"]) != "" {
			return nil, fmt.Errorf("use Brote tracepoints for captures; DAP logpoints are unsupported")
		}
		location := protocol.Location{File: file, Line: num(item["line"])}
		if functions {
			location = protocol.Location{Function: str(item["name"])}
		}
		d := protocol.Definition{ID: session.NewID(16), Revision: 1, Owner: "editor", Kind: "breakpoint", Enabled: true, Scope: protocol.Scope{Workspace: b.s.Project, Session: b.s.ID}, Location: location, Condition: str(item["condition"]), HitCondition: str(item["hitCondition"])}
		for i, previous := range old {
			if previous.Location == location {
				d.ID, d.Revision = previous.ID, previous.Revision
				if !reflect.DeepEqual(d, previous) {
					d.Revision++
				}
				old = append(old[:i], old[i+1:]...)
				break
			}
		}
		if err := definitions.Validate(d, b.s.ID, b.s.RunID, b.s.Project); err != nil {
			return nil, err
		}
		if d.Condition != "" {
			if err := validateExpression(d.Condition); err != nil {
				return nil, err
			}
		}
		next.Items = append(next.Items, d)
		ids = append(ids, d.ID)
	}
	if len(next.Items) > definitions.Limit {
		return nil, fmt.Errorf("definition limit reached")
	}
	next.Revision++
	previous := b.s.Definitions
	b.s.Definitions = next
	if err := b.persist(); err != nil {
		b.s.Definitions = previous
		return nil, err
	}
	if err := b.reconcileDefinitions(); err != nil {
		return nil, err
	}
	out := []any{}
	for _, id := range ids {
		for _, r := range b.resolutions {
			if r.DefinitionID == id {
				out = append(out, obj{"id": r.AdapterID, "verified": r.Verified, "line": r.Resolved.Line, "source": obj{"path": r.Resolved.File}, "message": r.Message})
			}
		}
	}
	return obj{"breakpoints": out}, nil
}

// Compatibility commands enter the same definition store; they never create a
// second owner of Delve state. Stable-ID CRUD is preferred for concurrent edits.
func (b *broker) legacyDefinitionAction(a obj) (obj, error) {
	next := b.s.Definitions.Copy()
	var selected protocol.Definition
	var err error
	if str(a["action"]) == "break" {
		location := protocol.Location{Function: str(a["function"])}
		if location.Function == "" {
			file := str(a["file"])
			if !filepath.IsAbs(file) {
				file = filepath.Join(b.s.Project, file)
			}
			location = protocol.Location{File: filepath.Clean(file), Line: num(a["line"])}
		}
		selected = protocol.Definition{ID: session.NewID(16), Owner: "cli", Kind: "breakpoint", Enabled: true, Scope: protocol.Scope{Workspace: b.s.Project, Session: b.s.ID}, Location: location, Condition: str(a["condition"]), HitCondition: str(a["hitCondition"])}
		if err = definitions.Validate(selected, b.s.ID, b.s.RunID, b.s.Project); err != nil {
			return nil, err
		}
		if selected.Condition != "" {
			if err = validateExpression(selected.Condition); err != nil {
				return nil, err
			}
		}
		next, err = next.Put(selected, 0)
	} else {
		for _, r := range b.resolutions {
			if r.AdapterID == num(a["breakpoint"]) {
				for _, d := range next.Items {
					if d.ID == r.DefinitionID && d.Owner == "cli" && d.Kind == "breakpoint" {
						if selected.ID != "" {
							return nil, fmt.Errorf("multiple definitions share this adapter ID; remove by stable ID")
						}
						selected = d
					}
				}
			}
		}
		if selected.ID == "" {
			return nil, fmt.Errorf("CLI breakpoint not found; use definition CRUD with its owner and stable ID")
		}
		next, err = next.Delete(selected.ID, selected.Owner, selected.Revision)
	}
	if err != nil {
		return nil, err
	}
	previous := b.s.Definitions
	b.s.Definitions = next
	if err = b.persist(); err != nil {
		b.s.Definitions = previous
		return nil, err
	}
	b.generation++
	if err = b.reconcileDefinitions(); err != nil {
		return nil, err
	}
	result := obj{"definition": selected}
	for _, r := range b.resolutions {
		if r.DefinitionID == selected.ID {
			result["Breakpoint"] = obj{"id": r.AdapterID, "verified": r.Verified, "file": r.Resolved.File, "line": r.Resolved.Line, "functionName": selected.Location.Function}
			break
		}
	}
	return result, nil
}
