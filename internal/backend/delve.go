// Package backend translates a DAP debug adapter into the broker's v2 snapshot
// model. Delve-only metadata gaps are isolated here during the RPC migration.
package backend

import (
	"agentdebugger/internal/dap"
	"agentdebugger/internal/delve"
	"agentdebugger/internal/protocol"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Delve struct {
	client      *dap.Client
	address     string
	mu          sync.Mutex
	bpMu        sync.Mutex
	epoch       uint64
	state       obj
	caps        obj
	stopped     chan struct{}
	done        chan struct{}
	breakpoints []any
	Events      chan obj
}

var ErrRunning = errors.New("DAP remote attach would halt the running target")

func Open(address string, owners map[string]string, functions map[string]string) (*Delve, error) {
	return OpenWithMapping(address, owners, functions, nil)
}
func OpenWithMapping(address string, owners map[string]string, functions map[string]string, mapping []protocol.PathMapping) (*Delve, error) {
	// DAP remote attach does not report PID or running state. Read identity once,
	// before opening the DAP session; all subsequent execution uses DAP.
	raw, err := delve.Call(address, "State", obj{"NonBlocking": true}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if truth(asObj(raw["State"])["Running"]) {
		return nil, ErrRunning
	}
	c, err := dap.Dial(address)
	if err != nil {
		return nil, err
	}
	d := &Delve{client: c, address: address, state: asObj(raw["State"]), stopped: make(chan struct{}), Events: make(chan obj, 256), done: make(chan struct{})}
	initial, err := delve.Call(address, "ListBreakpoints", obj{"All": false}, 5*time.Second)
	if err != nil {
		c.Close()
		return nil, err
	}
	for _, raw := range asList(initial["Breakpoints"]) {
		bp := asObj(raw)
		if num(bp["id"]) <= 0 {
			continue
		}
		owner := owners[fmt.Sprint(num(bp["id"]))]
		if owner == "" {
			owner = fmt.Sprintf("restored-%d", num(bp["id"]))
		}
		bp["client"] = owner
		expression := functions[fmt.Sprint(num(bp["id"]))]
		if expression == "" && strings.HasPrefix(str(bp["name"]), "functionBreakpoint Name=") {
			expression = strings.TrimPrefix(str(bp["name"]), "functionBreakpoint Name=")
		}
		bp["kind"] = "source"
		if expression != "" {
			bp["kind"] = "function"
			bp["functionName"] = expression
		}
		d.breakpoints = append(d.breakpoints, bp)
	}
	go d.events()
	d.caps, err = d.Request("initialize", obj{"adapterID": "go", "clientID": "agentdebugger", "pathFormat": "path", "linesStartAt1": true, "columnsStartAt1": true, "supportsVariableType": true, "supportsVariablePaging": true})
	if err == nil {
		_, err = d.Request("attach", obj{"mode": "remote", "stopOnEntry": true, "substitutePath": mapping})
	}
	if err == nil {
		_, err = d.Request("configurationDone", obj{})
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return d, nil
}
func (d *Delve) Close()            { d.client.Close() }
func (d *Delve) Capabilities() obj { return d.caps }
func (d *Delve) Begin(command string, args obj) (func() (obj, error), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	wait, err := d.client.Begin(ctx, command, args)
	if err != nil {
		cancel()
		return nil, err
	}
	return func() (obj, error) { defer cancel(); return wait() }, nil
}
func (d *Delve) Request(command string, args obj) (obj, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return d.client.Request(ctx, command, args)
}
func (d *Delve) events() {
	defer close(d.Events)
	defer close(d.done)
	for e := range d.client.Events {
		body := asObj(e["body"])
		d.mu.Lock()
		switch str(e["event"]) {
		case "continued":
			d.epoch++
			d.state["Running"] = true
		case "stopped":
			if str(body["reason"]) == "entry" && truth(d.state["Running"]) {
				d.mu.Unlock()
				continue
			}
			d.epoch++
			d.state["Running"] = false
			d.state["stopDescription"] = body["description"]
			d.state["stopText"] = body["text"]
			d.state["stopReason"] = body["reason"]
			if str(body["reason"]) != "entry" {
				d.state["currentGoroutine"] = obj{"id": body["threadId"]}
			}
			close(d.stopped)
			d.stopped = make(chan struct{})
		case "exited", "terminated":
			d.epoch++
			d.state["Running"] = false
			d.state["exited"] = true
			if code, ok := body["exitCode"]; ok {
				d.state["exitStatus"] = code
			}
			close(d.stopped)
			d.stopped = make(chan struct{})
		}
		d.mu.Unlock()
		d.Events <- e
	}
}
func (d *Delve) State() obj {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := obj{}
	for k, v := range d.state {
		out[k] = v
	}
	return out
}
func (d *Delve) frame(ctx context.Context, gid, index int) (int, error) {
	if gid <= 0 {
		gid = num(asObj(d.State()["currentGoroutine"])["id"])
	}
	v, e := d.request(ctx, "stackTrace", obj{"threadId": gid, "startFrame": index, "levels": 1})
	if e != nil {
		return 0, e
	}
	f := asList(v["stackFrames"])
	if len(f) == 0 {
		return 0, fmt.Errorf("frame unavailable")
	}
	return num(asObj(f[0])["id"]), nil
}

// Call preserves the existing public snapshot shape while migrating transport.
func (d *Delve) Call(method string, a obj) (obj, error) {
	timeout := 2 * time.Second
	if method == "Detach" {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return d.CallContext(ctx, method, a)
}
func (d *Delve) CallContext(ctx context.Context, method string, a obj) (obj, error) {
	switch method {
	case "State":
		s := d.State()
		if !truth(s["Running"]) && !truth(s["exited"]) {
			gid := num(asObj(s["currentGoroutine"])["id"])
			v, e := d.CallContext(ctx, "Stacktrace", obj{"Id": gid, "Depth": 1})
			if e == nil && len(asList(v["Locations"])) > 0 {
				f := asObj(asList(v["Locations"])[0])
				s["currentThread"] = obj{"file": f["file"], "line": f["line"], "function": f["function"], "goroutineID": gid}
			}
		}
		return obj{"State": s}, nil
	case "Stacktrace":
		gid := num(a["Id"])
		if gid <= 0 {
			gid = num(asObj(d.State()["currentGoroutine"])["id"])
		}
		// A pre-runtime process entry has no goroutine yet. Delve's DAP
		// placeholder thread is not a valid stack and can return synthetic ??? frames.
		if gid <= 0 {
			return obj{"Locations": []any{}}, nil
		}
		v, e := d.request(ctx, "stackTrace", obj{"threadId": gid, "startFrame": max(0, num(a["Start"])), "levels": min(128, max(1, num(a["Depth"])))})
		out := []any{}
		for _, raw := range asList(v["stackFrames"])[:min(len(asList(v["stackFrames"])), 128)] {
			f := asObj(raw)
			out = append(out, obj{"file": asObj(f["source"])["path"], "line": f["line"], "function": obj{"name": f["name"]}, "id": f["id"]})
		}
		return obj{"Locations": out, "totalFrames": v["totalFrames"]}, e
	case "ListGoroutines":
		v, e := d.request(ctx, "threads", obj{})
		out := []any{}
		threads := asList(v["threads"])
		start := min(len(threads), max(0, num(a["Start"])))
		end := min(len(threads), start+min(256, max(1, num(a["Count"]))))
		for _, raw := range threads[start:end] {
			t := asObj(raw)
			out = append(out, obj{"id": t["id"], "name": t["name"]})
		}
		next := 0
		if end < len(threads) {
			next = end
		}
		return obj{"Goroutines": out, "Nextg": next}, e
	case "ExceptionInfo":
		return d.request(ctx, "exceptionInfo", a)
	case "ListSources":
		return delve.Call(d.address, method, a, 5*time.Second) // Delve has no loadedSources implementation yet.
	case "ListLocalVars", "ListFunctionArgs":
		if method == "ListFunctionArgs" {
			return obj{"Args": []any{}}, nil
		} // DAP exposes a single locals scope, including arguments.
		scope := asObj(a["Scope"])
		id, e := d.frame(ctx, num(scope["GoroutineID"]), num(scope["Frame"]))
		if e != nil {
			return nil, e
		}
		scopes, e := d.request(ctx, "scopes", obj{"frameId": id})
		if e != nil {
			return nil, e
		}
		out := []any{}
		budget := newValueBudget()
		for _, raw := range asList(scopes["scopes"]) {
			s := asObj(raw)
			if !strings.Contains(strings.ToLower(str(s["name"])), "local") {
				continue
			}
			vars, e := d.request(ctx, "variables", obj{"variablesReference": s["variablesReference"], "start": 0, "count": 128})
			if e != nil {
				return nil, e
			}
			for _, v := range asList(vars["variables"]) {
				if budget.nodes <= 0 || budget.bytes <= 0 {
					budget.truncated = true
					break
				}
				item, e := d.variable(ctx, asObj(v), 1, 32, budget)
				if e != nil {
					return nil, e
				}
				out = append(out, item)
			}
		}
		return obj{"Variables": out, "truncated": budget.truncated}, nil
	case "Eval":
		s := asObj(a["Scope"])
		id, e := d.frame(ctx, num(s["GoroutineID"]), num(s["Frame"]))
		if e != nil {
			return nil, e
		}
		v, e := d.request(ctx, "evaluate", obj{"frameId": id, "expression": a["Expr"], "context": "watch"})
		if e != nil {
			return nil, e
		}
		v["name"] = a["Expr"]
		cfg := asObj(a["Cfg"])
		item, e := d.variable(ctx, v, num(cfg["MaxVariableRecurse"]), max(1, num(cfg["MaxArrayValues"])), newValueBudget())
		return obj{"Variable": item}, e
	case "Command":
		names := map[string]string{"continue": "continue", "next": "next", "step": "stepIn", "stepOut": "stepOut", "halt": "pause"}
		name := names[str(a["name"])]
		if name == "" {
			return nil, fmt.Errorf("unsupported execution command")
		}
		d.mu.Lock()
		wait := d.stopped
		gid := num(asObj(d.state["currentGoroutine"])["id"])
		d.mu.Unlock()
		_, e := d.request(ctx, name, obj{"threadId": gid})
		if e != nil {
			return nil, e
		}
		if name != "pause" {
			select {
			case <-wait:
			case <-d.done:
				return nil, fmt.Errorf("backend disconnected while executing")
			}
		}
		return obj{"State": d.State()}, nil
	case "Detach":
		if !truth(a["Kill"]) {
			// In Delve --accept-multiclient mode, DAP disconnect(false) only
			// closes that client and leaves the target traced and paused.
			// Actual process detach is a backend lifecycle operation, owned
			// here by the service, and must finish before Delve is shut down.
			// Pause is safe at an existing stop and also settles an in-flight
			// continue whose continued event has not reached our state yet.
			if !truth(d.State()["exited"]) {
				if _, err := d.request(ctx, "pause", obj{"threadId": num(asObj(d.State()["currentGoroutine"])["id"])}); err != nil {
					return nil, err
				}
			}
			timeout := 8 * time.Second
			if deadline, ok := ctx.Deadline(); ok {
				timeout = min(timeout, time.Until(deadline))
			}
			if timeout <= 0 {
				return nil, ctx.Err()
			}
			return delve.Call(d.address, "Detach", obj{"Kill": false}, timeout)
		}
		return d.request(ctx, "disconnect", obj{"terminateDebuggee": true})
	case "CreateBreakpoint":
		return d.createBreakpoint(a)
	case "ClearBreakpoint":
		return d.clearBreakpoint(num(a["Id"]))
	case "ListBreakpoints":
		d.mu.Lock()
		defer d.mu.Unlock()
		return obj{"Breakpoints": append([]any{}, d.breakpoints...)}, nil
	}
	return nil, fmt.Errorf("unsupported backend operation %s", method)
}
