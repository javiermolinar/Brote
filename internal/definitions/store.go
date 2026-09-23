// Package definitions owns durable debugger intent. Adapter identifiers and
// resolved locations deliberately do not belong to this store.
package definitions

import (
	"agentdebugger/internal/protocol"
	"fmt"
	"path/filepath"
)

const Limit = 512

type Store struct {
	Revision uint64                `json:"revision"`
	Items    []protocol.Definition `json:"items"`
}

func (s Store) Copy() Store {
	result := Store{Revision: s.Revision, Items: append([]protocol.Definition{}, s.Items...)}
	for i := range result.Items {
		result.Items[i].Values = map[string]string{}
		for k, v := range s.Items[i].Values {
			result.Items[i].Values[k] = v
		}
	}
	return result
}
func Validate(d protocol.Definition, session, run, project string) error {
	if d.ID == "" || len(d.ID) > 128 || d.Owner == "" || len(d.Owner) > 128 {
		return fmt.Errorf("definition id and owner must contain 1–128 bytes")
	}
	if d.Scope.Workspace != project || d.Scope.Session != session || (d.Scope.Run != "" && d.Scope.Run != run) {
		return fmt.Errorf("definition scope must identify this workspace/session and optionally the current run")
	}
	if d.Kind != "breakpoint" && d.Kind != "tracepoint" {
		return fmt.Errorf("unsupported definition kind")
	}
	if d.Location.Function != "" {
		if d.Kind == "tracepoint" {
			return fmt.Errorf("function-call tracing is unsupported; choose a source-line tracepoint")
		}
		if d.Location.File != "" || d.Location.Line != 0 || len(d.Location.Function) > 1024 {
			return fmt.Errorf("invalid function breakpoint location")
		}
	} else if !filepath.IsAbs(d.Location.File) || d.Location.Line <= 0 {
		return fmt.Errorf("source definitions require an absolute path and positive line")
	}
	if len(d.Location.File) > 4096 || len(d.Condition) > 4096 || len(d.HitCondition) > 128 || len(d.Name) > 256 {
		return fmt.Errorf("definition text exceeds limits")
	}
	if d.CaptureLimit < 0 || d.CaptureLimit > 10000 || (d.Kind == "tracepoint" && d.CaptureLimit == 0) {
		return fmt.Errorf("tracepoint captureLimit must be 1–10000")
	}
	if len(d.Values) > 16 {
		return fmt.Errorf("at most 16 selected values are supported")
	}
	for name, expr := range d.Values {
		if name == "" || len(name) > 128 || expr == "" || len(expr) > 4096 {
			return fmt.Errorf("invalid selected value")
		}
	}
	if d.Kind == "breakpoint" && (len(d.Values) > 0 || d.CaptureLimit != 0) {
		return fmt.Errorf("capture settings require a tracepoint")
	}
	return nil
}
func (s Store) Put(d protocol.Definition, expected uint64) (Store, error) {
	next := s.Copy()
	d = (Store{Items: []protocol.Definition{d}}).Copy().Items[0]
	for i, old := range next.Items {
		if old.ID == d.ID {
			if old.Owner != d.Owner {
				return s, &protocol.Error{Code: "ownership_mismatch", Message: "definition belongs to another client"}
			}
			if expected != old.Revision {
				return s, &protocol.Error{Code: "stale_revision", Message: "stale definition revision"}
			}
			d.Revision = old.Revision + 1
			next.Items[i] = d
			next.Revision++
			return next, nil
		}
	}
	if expected != 0 {
		return s, &protocol.Error{Code: "stale_revision", Message: "definition no longer exists"}
	}
	if len(next.Items) >= Limit {
		return s, fmt.Errorf("definition limit reached")
	}
	d.Revision = 1
	next.Items = append(next.Items, d)
	next.Revision++
	return next, nil
}
func (s Store) Delete(id, owner string, expected uint64) (Store, error) {
	next := s.Copy()
	for i, old := range next.Items {
		if old.ID == id {
			if old.Owner != owner || old.Revision != expected {
				return s, &protocol.Error{Code: "stale_revision", Message: "owner or definition revision changed"}
			}
			next.Items = append(next.Items[:i], next.Items[i+1:]...)
			next.Revision++
			return next, nil
		}
	}
	return s, &protocol.Error{Code: "not_found", Message: "definition not found"}
}
func (s Store) ForRun(run string) Store {
	next := Store{Revision: s.Revision}
	for _, d := range s.Items {
		if d.Scope.Run == "" || d.Scope.Run == run {
			next.Items = append(next.Items, d)
		}
	}
	if len(next.Items) != len(s.Items) {
		next.Revision++
	}
	return next
}
