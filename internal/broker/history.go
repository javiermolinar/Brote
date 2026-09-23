package broker

import (
	"agentdebugger/internal/session"
	"agentdebugger/internal/tracing"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// All broker history calls run under b.mu (or during single-threaded startup).
func (b *broker) record(kind, actor string, data any) {
	if kind == "session.ended" || kind == "target_exited" {
		b.traces.Event(tracing.Event{Kind: "exited", AllThreads: true})
	}
	payload, _ := json.Marshal(data)
	b.traces.Event(tracing.Event{Kind: "record", Command: kind, Data: payload})
	if b.history == nil {
		return
	}
	if err := b.history.Append(kind, actor, data); err != nil {
		b.historyError = err.Error()
	}
}
func (b *broker) historyStop(reason string) {
	b.traces.Event(tracing.Event{Kind: "stopped", AllThreads: true})
	if b.history == nil {
		return
	}
	b.stopID = session.NewID(8)
	b.captured = map[string]bool{}
	if reason == "" {
		reason = "stopped"
	}
	data := obj{"stop_id": b.stopID, "reason": reason}
	state, err := b.state()
	if err == nil {
		gid := num(asObj(state["currentGoroutine"])["id"])
		data["context_id"] = fmt.Sprint(gid)
		data["location"] = pick(asObj(state["currentThread"]), "file", "line")
		stack, e := b.rpc("Stacktrace", obj{"Id": gid, "Depth": 30, "Full": false})
		if e == nil {
			view := obj{"goroutine": gid, "frames": stack["Locations"]}
			b.traceInspection(view)
			snapshot := b.historyContext(view)
			if ref, e := b.history.Snapshot(snapshot); e == nil {
				data["snapshot"] = ref
			} else {
				b.historyError = e.Error()
			}
		} else {
			data["capture_error"] = e.Error()
		}
	} else {
		data["capture_error"] = err.Error()
	}
	b.record("execution.stopped", "debugger", data)
}
func (b *broker) historyContext(v obj) obj {
	frames := []any{}
	observations := []any{}
	for i, item := range asList(v["frames"]) {
		f := asObj(item)
		frames = append(frames, obj{"index": i, "function": asObj(f["function"])["name"], "file": f["file"], "line": f["line"]})
		for _, scope := range []string{"Locals", "Arguments"} {
			for _, value := range asList(f[scope]) {
				observations = append(observations, obj{"frame_index": i, "scope": scope, "value": value})
			}
		}
	}
	return obj{"v": 1, "stop_id": b.stopID, "contexts": []any{obj{"id": fmt.Sprint(num(v["goroutine"])), "kind": "goroutine", "selected": true, "selected_frame": num(v["frame"]), "frames": frames, "observations": observations}}, "source": v["source"], "source_identity": v["sourceIdentity"], "watches": v["watches"]}
}
func (b *broker) historyInspection(v obj) {
	if b.history == nil || v["status"] != "paused" {
		return
	}
	snapshot := b.historyContext(v)
	ref, err := b.history.Snapshot(snapshot)
	if err != nil {
		b.historyError = err.Error()
		return
	}
	if b.captured == nil {
		b.captured = map[string]bool{}
	}
	if b.captured[ref] {
		return
	}
	b.captured[ref] = true
	b.traceInspection(v)
	b.record("inspection.captured", "observer", obj{"stop_id": b.stopID, "context_id": fmt.Sprint(num(v["goroutine"])), "snapshot": ref})
}
func (b *broker) historyAction(a, result obj, err error) {
	if b.history == nil {
		return
	}
	data := obj{"stop_id": b.stopID, "request": pick(a, "action", "file", "line", "function", "condition", "hitCondition", "breakpoint", "expression", "frame", "depth", "count", "note", "status", "event"), "result": result}
	if gid := num(a["goroutine"]); gid != 0 {
		data["context_id"] = fmt.Sprint(gid)
	}
	kind := "action.completed"
	switch str(a["action"]) {
	case "continue", "next", "step", "stepout", "pause":
		kind = "execution.requested"
	case "break", "clear":
		kind = "breakpoint.changed"
	case "eval":
		kind = "inspection.evaluated"
	}
	if err != nil {
		kind = "action.failed"
		data["error"] = err.Error()
	}
	b.record(kind, str(a["actor"]), data)
}
func (b *broker) historyDiscussion(d session.Discussion, action string) {
	if b.history == nil {
		return
	}
	// Keep original discussion IDs and captured contexts; the archive copy also
	// remains readable through the existing offline comment command.
	if err := session.Write(filepath.Join(b.history.Dir, "discussion.json"), d); err != nil {
		b.historyError = err.Error()
	}
	ref, err := b.history.Snapshot(d)
	if err != nil {
		b.historyError = err.Error()
		return
	}
	actor := "human"
	if action == "reply" || action == "delivery" {
		actor = "agent"
		if b.s.Binding != nil {
			actor += ":" + b.s.Binding.Name
		}
	}
	if action == "restored" {
		actor = "core"
	}
	b.record("discussion."+action, actor, obj{"snapshot": ref, "stop_id": b.stopID})
}

// Convert debugger observations at the broker boundary, independent of its client.
func (b *broker) traceInspection(v obj) {
	if b.traces == nil {
		return
	}
	frames := []tracing.Frame{}
	for _, value := range asList(v["frames"]) {
		f := asObj(value)
		frame := tracing.Frame{Name: str(asObj(f["function"])["name"]), Line: num(f["line"])}
		if path := str(f["file"]); path != "" {
			frame.Source = &struct {
				Path string `json:"path"`
			}{path}
		}
		frames = append(frames, frame)
	}
	selected := tracing.Frame{}
	index := num(v["frame"])
	if index >= 0 && index < len(frames) {
		selected = frames[index]
	}
	scopes := []json.RawMessage{}
	rawFrames := asList(v["frames"])
	if index >= 0 && index < len(rawFrames) {
		for _, name := range []string{"Locals", "Arguments"} {
			if values := asObj(rawFrames[index])[name]; values != nil {
				data, _ := json.Marshal(obj{"name": name, "variables": values})
				scopes = append(scopes, data)
			}
		}
	}
	if values := v["watches"]; values != nil {
		data, _ := json.Marshal(obj{"name": "watches", "variables": values})
		scopes = append(scopes, data)
	}
	b.traces.Event(tracing.Event{Kind: "snapshot", Observation: &tracing.Observation{Thread: num(v["goroutine"]), Frame: selected, Stack: frames, Scopes: scopes, CapturedAt: time.Now()}})
}
