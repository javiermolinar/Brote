package broker

import (
	"agentdebugger/internal/definitions"
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"fmt"
)

type serviceRequest struct {
	Goroutine int `json:"goroutine,omitempty"`
	Start     int `json:"start,omitempty"`
	Count     int `json:"count,omitempty"`
	protocol.Request
	Definition *protocol.Definition `json:"definition,omitempty"`
	ID         string               `json:"id,omitempty"`
	Revision   uint64               `json:"revision,omitempty"`
}

func (b *broker) identity() protocol.Identity {
	return protocol.Identity{Session: b.s.ID, Run: b.s.RunID, Generation: b.generation, PauseEpoch: b.handleEpoch}
}
func (b *broker) service(r serviceRequest) (obj, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.s.ServiceVersion != protocol.Version || b.closing || b.s.Stopped {
		return nil, fmt.Errorf("shared service unavailable")
	}
	if err := r.Check(b.identity(), map[string]bool{"definition.list": true, "definition.put": true, "definition.delete": true, "capture.list": true, "inspection.goroutines": true, "inspection.stack": true}, false); err != nil {
		return nil, err
	}
	if r.Operation == "inspection.goroutines" || r.Operation == "inspection.stack" {
		return b.serviceInspect(r)
	}
	if r.Operation == "capture.list" {
		return obj{"version": 1, "identity": b.identity(), "captures": b.captureView(), "pending": b.capturePending}, nil
	}
	var err error
	next := b.s.Definitions.Copy()
	switch r.Operation {
	case "definition.list":
	case "definition.put":
		if r.Definition == nil {
			return nil, fmt.Errorf("definition is required")
		}
		d := *r.Definition
		if d.ID == "" {
			d.ID = session.NewID(16)
		}
		if d.Owner != "" && d.Owner != r.Client {
			return nil, fmt.Errorf("definition owner must match client")
		}
		d.Owner = r.Client
		if err = definitions.Validate(d, b.s.ID, b.s.RunID, b.s.Project); err != nil {
			return nil, err
		}
		if d.Condition != "" {
			if err = validateExpression(d.Condition); err != nil {
				return nil, err
			}
		}
		for _, expr := range d.Values {
			if err = validateExpression(expr); err != nil {
				return nil, err
			}
		}
		next, err = next.Put(d, r.Revision)
	case "definition.delete":
		next, err = next.Delete(r.ID, r.Client, r.Revision)
	}
	if err != nil {
		return nil, err
	}
	if r.Operation != "definition.list" {
		if b.backend != nil && (b.moving || b.interrupting || truth(b.backend.State()["Running"])) {
			return nil, fmt.Errorf("pause before changing definitions")
		}
		old := b.s.Definitions
		b.s.Definitions = next
		if err = b.persist(); err != nil {
			b.s.Definitions = old
			return nil, err
		}
		b.generation++
		if err = b.reconcileDefinitions(); err != nil {
			b.lastError = err.Error()
			return nil, err
		}
		_ = b.emit("definitions_changed", r.Operation)
	}
	return obj{"version": protocol.Version, "identity": b.identity(), "definitions": next, "resolutions": b.resolutions}, nil
}
