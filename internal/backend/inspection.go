package backend

import (
	"context"
	"fmt"
)

// Epoch is updated on receipt of backend movement events, independently of the
// broker event consumer. It fences observations even while that consumer waits.
func (d *Delve) Epoch() uint64 { d.mu.Lock(); defer d.mu.Unlock(); return d.epoch }
func (d *Delve) request(ctx context.Context, command string, args obj) (obj, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.client.Request(ctx, command, args)
}
func (d *Delve) Inspect(ctx context.Context, command string, args obj) (obj, error) {
	epoch := d.Epoch()
	if truth(d.State()["Running"]) || truth(d.State()["exited"]) {
		return nil, fmt.Errorf("inspection requires a paused target")
	}
	body, err := d.request(ctx, command, args)
	if d.Epoch() != epoch {
		return nil, fmt.Errorf("target moved during inspection; refresh the paused state")
	}
	return body, err
}

type valueBudget struct {
	nodes, bytes int
	truncated    bool
}

func newValueBudget() *valueBudget { return &valueBudget{nodes: 128, bytes: 32768} }
func (b *valueBudget) text(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	n := min(len(s), min(4096, max(0, b.bytes)))
	b.bytes -= n
	if n < len(s) {
		b.truncated = true
	}
	return s[:n]
}
func (d *Delve) variable(ctx context.Context, v obj, depth, count int, budget *valueBudget) (obj, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if budget.nodes <= 0 || budget.bytes <= 0 {
		budget.truncated = true
		return obj{"truncated": true}, nil
	}
	budget.nodes--
	value := v["value"]
	if value == nil {
		value = v["result"]
	}
	before := budget.bytes
	out := obj{"name": budget.text(v["name"]), "type": budget.text(v["type"]), "value": budget.text(value)}
	if s, ok := value.(string); ok && (len(s) > 4096 || len(s) > before) {
		out["truncated"] = true
	}
	ref := num(v["variablesReference"])
	if ref <= 0 {
		return out, nil
	}
	size := max(1, num(v["namedVariables"])+num(v["indexedVariables"]))
	out["kind"], out["len"] = 23, size
	if depth <= 0 || budget.nodes <= 0 || budget.bytes <= 0 {
		out["truncated"] = true
		budget.truncated = true
		return out, nil
	}
	count = min(max(1, count), budget.nodes)
	children, err := d.request(ctx, "variables", obj{"variablesReference": ref, "start": 0, "count": count})
	if err != nil {
		out["error"] = err.Error()
		return out, nil
	}
	items := []any{}
	for _, raw := range asList(children["variables"]) {
		if len(items) >= count || budget.nodes <= 0 || budget.bytes <= 0 {
			break
		}
		child, err := d.variable(ctx, asObj(raw), depth-1, count, budget)
		if err != nil {
			out["error"] = err.Error()
			break
		}
		items = append(items, child)
	}
	out["children"] = items
	if len(items) < size || len(items) < len(asList(children["variables"])) {
		out["truncated"] = true
		budget.truncated = true
	}
	return out, nil
}
