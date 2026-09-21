package backend

import "fmt"

// ReplaceBreakpoints merges a frontend's desired list with breakpoints owned by
// other clients. Delve's setBreakpoints replaces the entire file/function set.
func (d *Delve) ReplaceBreakpoints(owner, file string, requested []any, functions bool) (obj, error) {
	d.bpMu.Lock()
	defer d.bpMu.Unlock()
	d.mu.Lock()
	previous := append([]any{}, d.breakpoints...)
	d.mu.Unlock()
	merged := []any{}
	for _, raw := range previous {
		bp := asObj(raw)
		same := str(bp["kind"]) != "function" && str(bp["file"]) == file
		if functions {
			same = str(bp["kind"]) == "function"
		}
		if same && str(bp["client"]) == owner {
			continue
		}
		merged = append(merged, bp)
	}
	for _, raw := range requested {
		r := asObj(raw)
		bp := obj{"kind": "source", "file": file, "line": r["line"], "Cond": r["condition"], "HitCond": r["hitCondition"], "client": owner, "name": r["name"]}
		if functions {
			bp["functionName"] = r["name"]
			bp["kind"] = "function"
		}
		merged = append(merged, bp)
	}
	args := []any{}
	indexes := []int{}
	for i, raw := range merged {
		bp := asObj(raw)
		same := str(bp["kind"]) != "function" && str(bp["file"]) == file
		if functions {
			same = str(bp["kind"]) == "function"
		}
		if !same {
			continue
		}
		args = append(args, obj{"line": bp["line"], "condition": bp["Cond"], "hitCondition": bp["HitCond"], "name": bp["functionName"]})
		indexes = append(indexes, i)
	}
	command := "setBreakpoints"
	input := obj{"source": obj{"path": file}, "breakpoints": args}
	if functions {
		command = "setFunctionBreakpoints"
		input = obj{"breakpoints": args}
	}
	response, err := d.Request(command, input)
	if err != nil {
		return nil, err
	}
	results := asList(response["breakpoints"])
	out := []any{}
	for j, i := range indexes {
		if j >= len(results) {
			return nil, fmt.Errorf("incomplete breakpoint response")
		}
		result := asObj(results[j])
		bp := asObj(merged[i])
		bp["id"] = result["id"]
		if result["line"] != nil {
			bp["line"] = result["line"]
		}
		bp["verified"] = result["verified"]
		if str(bp["client"]) == owner {
			out = append(out, result)
		}
	}
	d.mu.Lock()
	d.breakpoints = merged
	d.mu.Unlock()
	return obj{"breakpoints": out}, nil
}
func (d *Delve) createBreakpoint(a obj) (obj, error) {
	bp := asObj(a["Breakpoint"])
	file := str(bp["file"])
	fn := str(a["LocExpr"])
	owner := str(bp["name"])
	if owner == "" {
		owner = "agent"
	}
	request := obj{"line": bp["line"], "condition": bp["Cond"], "hitCondition": bp["HitCond"], "name": fn}
	v, e := d.ReplaceBreakpoints(owner, file, []any{request}, fn != "")
	if e != nil {
		return nil, e
	}
	items := asList(v["breakpoints"])
	if len(items) == 0 || !truth(asObj(items[0])["verified"]) {
		return nil, fmt.Errorf("breakpoint not verified: %v", items)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	id := num(asObj(items[0])["id"])
	for _, raw := range d.breakpoints {
		if num(asObj(raw)["id"]) == id {
			return obj{"Breakpoint": raw}, nil
		}
	}
	return nil, fmt.Errorf("breakpoint unavailable")
}
func (d *Delve) clearBreakpoint(id int) (obj, error) {
	d.mu.Lock()
	var selected obj
	for _, raw := range d.breakpoints {
		if num(asObj(raw)["id"]) == id {
			selected = asObj(raw)
			break
		}
	}
	d.mu.Unlock()
	if selected == nil {
		return nil, fmt.Errorf("breakpoint not found")
	}
	requested := []any{}
	d.mu.Lock()
	for _, raw := range d.breakpoints {
		bp := asObj(raw)
		if num(bp["id"]) == id || str(bp["client"]) != str(selected["client"]) || (str(bp["kind"]) != str(selected["kind"]) || (str(selected["kind"]) != "function" && str(bp["file"]) != str(selected["file"]))) {
			continue
		}
		requested = append(requested, obj{"line": bp["line"], "condition": bp["Cond"], "hitCondition": bp["HitCond"], "name": bp["functionName"]})
	}
	d.mu.Unlock()
	_, e := d.ReplaceBreakpoints(str(selected["client"]), str(selected["file"]), requested, str(selected["kind"]) == "function")
	return obj{"Breakpoint": selected}, e
}

func (d *Delve) BreakpointOwners() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]string{}
	for _, raw := range d.breakpoints {
		bp := asObj(raw)
		if num(bp["id"]) > 0 {
			out[fmt.Sprint(num(bp["id"]))] = str(bp["client"])
		}
	}
	return out
}

// FunctionBreakpoints preserves the original expression rather than a resolved source location.
func (d *Delve) FunctionBreakpoints() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]string{}
	for _, raw := range d.breakpoints {
		bp := asObj(raw)
		if str(bp["kind"]) == "function" {
			out[fmt.Sprint(num(bp["id"]))] = str(bp["functionName"])
		}
	}
	return out
}
