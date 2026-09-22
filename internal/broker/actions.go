package broker

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentdebugger/internal/delve"
	"agentdebugger/internal/editors"
	"agentdebugger/internal/editors/zed"
	"agentdebugger/internal/session"
)

func (b *broker) action(a obj) (result obj, actionErr error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	defer func() { b.historyAction(a, result, actionErr) }()
	// A human pause is always current intent; it must win over an agent step
	// which advanced the generation while the inspector request was in flight.
	humanInterrupt := human(str(a["actor"])) && (str(a["action"]) == "pause" || (str(a["action"]) == "task-cancel" && b.s.Task != nil && str(a["task"]) == b.s.Task.ID))
	if _, ok := a["generation"]; (!ok || num(a["generation"]) != b.generation) && !humanInterrupt {
		return nil, fmt.Errorf("session changed; refresh state before acting")
	}
	verb := str(a["action"])
	if str(a["actor"]) == "" {
		a["actor"] = "agent"
	}
	if str(a["actor"]) == "agent" && b.s.Binding != nil && str(a["binding"]) != b.s.Binding.ID && verb != "bind" && verb != "event-status" && verb != "eval" {
		return nil, fmt.Errorf("binding mismatch; refresh session binding")
	}
	if strings.HasPrefix(verb, "task-") {
		return b.coordinate(a)
	}
	if executionAction(verb) {
		if human(str(a["actor"])) {
			b.cancelTask("human debugger action")
		} else if str(a["actor"]) == "agent" {
			if err := b.taskValid(a); err != nil {
				return nil, err
			}
			b.s.Task.Status = "active"
			b.s.Task.Expires = time.Now().Add(taskLease).UTC().Format(time.RFC3339Nano)
		} else {
			return nil, fmt.Errorf("unknown execution actor")
		}
	}
	if verb == "editor-error" {
		if b.owner != "vscode" || str(a["handoverId"]) != b.s.HandoverID || b.peer != nil {
			return nil, fmt.Errorf("editor handover changed")
		}
		b.lastError = "VS Code: " + str(a["error"])
		if len(b.lastError) > 1024 {
			b.lastError = b.lastError[:1024]
		}
		return obj{"error": b.lastError}, nil
	}

	if verb == "event-status" {
		return b.eventStatus(a)
	}
	if verb == "bind" {
		b.cancelTask("agent binding changed")
		if err := b.interruptExecution(); err != nil {
			return nil, err
		}
		id := str(a["binding"])
		name := str(a["name"])
		if id == "" || len(id) > 256 || name == "" || len(name) > 80 {
			return nil, fmt.Errorf("binding (1–256 characters) and name (1–80) required")
		}
		revision := uint64(1)
		if b.s.Binding != nil {
			revision = b.s.Binding.Revision + 1
		}
		b.s.Binding = &session.Binding{ID: id, Name: name, Revision: revision}
		b.s.Notification = nil
		b.generation++
		return obj{"binding": b.s.Binding}, b.emit("binding_changed", "")
	}
	if verb == "retry-notification" {
		n := b.s.Notification
		if n == nil || (n.Status != "failed" && n.Status != "unknown") || b.owner != "agent" {
			return nil, fmt.Errorf("no current failed handback to retry")
		}
		return obj{"status": "pending"}, b.queueNotification("reclaim")
	}
	s, e := b.state()
	if e != nil && verb != "stop" {
		return nil, e
	}
	status := stateStatus(s, b.moving)
	if verb == "stop" {

		if b.peer != nil {
			b.peer.close()
		}
		_, e = b.rpc("Detach", obj{"Kill": true})
		if _, exited := delve.ExitState(e); e != nil && !exited {
			return nil, fmt.Errorf("could not stop Delve: %w", e)
		}
		b.s.Stopped = true
		_ = b.emit("terminated", "")
		cleanupErr := zed.RemoveConfig(b.s)
		b.generation++
		go func() { time.Sleep(150 * time.Millisecond); b.once.Do(func() { close(b.done) }) }()
		return obj{"status": "terminated", "detachError": errorString(e), "cleanupError": errorString(cleanupErr)}, nil
	}
	if status == "exited" {
		return nil, fmt.Errorf("program exited; stop this session and start another")
	}
	if verb == "eval" || verb == "watch" || verb == "unwatch" {
		if status != "paused" || truth(s["NextInProgress"]) {
			return nil, fmt.Errorf("inspection requires a settled pause")
		}
		expression := str(a["expression"])
		if verb == "eval" {
			return b.evaluate(expression, num(a["goroutine"]), num(a["frame"]), num(a["depth"]), num(a["count"]), s)
		}

		if e := validateExpression(expression); e != nil {
			return nil, e
		}
		list := []string{}
		found := false
		for _, w := range b.s.Watches {
			if w == expression {
				found = true
				if verb == "unwatch" {
					continue
				}
			}
			list = append(list, w)
		}
		if verb == "watch" && !found {
			if len(list) >= 16 {
				return nil, fmt.Errorf("at most 16 watches are supported")
			}
			list = append(list, expression)
		}
		b.s.Watches = list
		b.generation++
		return obj{"watches": list}, b.persist()
	}
	if verb == "reclaim" {
		if !editors.IsOwner(b.owner) && b.owner != "browser" {
			return nil, fmt.Errorf("Agent already has control")
		}
		if status != "paused" {
			return nil, fmt.Errorf("pause in %s before giving control to the agent", editors.Name(b.owner))
		}
		if truth(s["NextInProgress"]) {
			return nil, fmt.Errorf("a step is still in progress; settle it in the editor before handback")
		}
		if b.peer != nil {
			if b.peer.pendingCount() > 0 {
				return nil, fmt.Errorf("editor has requests in flight; wait for the pause to settle")
			}
			b.peer.close()
			b.peer = nil
		}
		b.owner = "agent"
		b.generation++
		out := obj{"owner": b.owner, "status": "paused", "message": "Editor detached; the same debuggee remains paused"}
		if err := b.emit("control_returned", str(a["note"])); err != nil {
			return nil, err
		}
		out["cursor"] = b.s.Cursor
		return out, nil
	}
	takeBrowser := str(a["actor"]) == "browser" && verb == "handover" && str(a["editor"]) == "browser"
	if verb == "pause" {
		b.generation++
		return obj{"status": "pause requested"}, b.interruptExecution()
	}
	if status != "paused" {
		return nil, fmt.Errorf("pause the program before %s", verb)
	}
	if truth(s["NextInProgress"]) && verb != "continue" {
		return nil, fmt.Errorf("a step is still in progress; continue to complete it before %s", verb)
	}
	switch verb {
	case "continue", "next", "step", "stepout":
		if err := b.beginExecution(verb, s); err != nil {
			return nil, err
		}
		return obj{"status": "running", "command": verb}, nil
	case "break":
		bp := obj{"name": "agent" + session.NewID(4), "Cond": str(a["condition"]), "HitCond": str(a["hitCondition"])}
		loc := str(a["function"])
		if loc == "" {
			file := str(a["file"])
			if file == "" || num(a["line"]) < 1 {
				return nil, fmt.Errorf("provide a file and positive line, or a function")
			}
			if !filepath.IsAbs(file) {
				file = filepath.Join(b.s.Project, file)
			}
			bp["file"] = filepath.Clean(file)
			bp["line"] = num(a["line"])
		}
		out, e := b.rpc("CreateBreakpoint", obj{"Breakpoint": bp, "LocExpr": loc})
		if e == nil {
			b.generation++
			b.notifyBreakpoint("new", asObj(out["Breakpoint"]))
			if b.backend != nil {
				e = b.persist()
			}
		}
		return out, e
	case "clear":
		id := num(a["breakpoint"])
		if id <= 0 {
			return nil, fmt.Errorf("positive breakpoint ID required")
		}
		out, e := b.rpc("ClearBreakpoint", obj{"Id": id})
		if e == nil {
			b.generation++
			b.notifyBreakpoint("removed", asObj(out["Breakpoint"]))
			if b.backend != nil {
				e = b.persist()
			}
		}
		return out, e
	case "handover":
		editor := str(a["editor"])
		if editor == "" {
			editor = b.s.Editor
		}
		if editor == "" {
			editor = "browser"
		}
		if !editors.IsOwner(editor) && editor != "browser" {
			return nil, fmt.Errorf("editor must be browser, zed or vscode")
		}
		if !takeBrowser && b.owner != "agent" && b.owner != "browser" && editor != b.owner {
			return nil, fmt.Errorf("reclaim before changing editors")
		}

		if editor == "browser" {
			if b.peer != nil {
				if b.peer.pendingCount() > 0 {
					return nil, fmt.Errorf("editor has requests in flight; wait for the pause to settle")
				}
				b.peer.close()
				b.peer = nil
			}
			b.owner, b.s.Editor = "browser", "browser"
			b.generation++
			if err := b.emit("ownership_changed", str(a["note"])); err != nil {
				return nil, err
			}
			return obj{"owner": b.owner, "panel": b.s.HTTP + "/", "cursor": b.s.Cursor}, nil
		}
		if editor == "vscode" {
			b.owner, b.s.Editor, b.s.HandoverID = editor, editor, session.NewID(8)
			b.lastError = ""
			b.generation++
			out := obj{"owner": editor, "status": "paused", "handoverId": b.s.HandoverID, "instructions": "The Brote companion extension will attach in VS Code for this project."}
			if err := b.emit("ownership_changed", str(a["note"])); err != nil {
				out["persistenceError"] = err.Error()
			}
			if truth(a["open"]) {
				cmd := exec.Command("code", b.s.Project)
				if err := cmd.Start(); err != nil {
					out["openError"] = err.Error()
				} else {
					go func() { _ = cmd.Wait() }()
				}
			}
			return out, nil
		}
		path, e := zed.WriteConfig(b.s)
		if e != nil {
			return nil, e
		}
		b.owner = "zed"
		b.s.Editor, b.s.HandoverID = "zed", session.NewID(8)
		b.lastError = ""
		b.generation++
		out := obj{"owner": "zed", "status": "paused", "config": path, "label": zed.Label(b.s.ID), "instructions": "In Zed, press F4 and choose " + zed.Label(b.s.ID)}
		if err := b.emit("ownership_changed", str(a["note"])); err != nil {
			out["persistenceError"] = err.Error()
		}
		if truth(a["open"]) {
			args := []string{b.s.Project}
			thread := asObj(s["currentThread"])
			if f := str(thread["file"]); f != "" {
				args = append(args, fmt.Sprintf("%s:%d", f, num(thread["line"])))
			}
			cmd := exec.Command("zed", args...)
			if e := cmd.Start(); e != nil {
				out["openError"] = e.Error()
			} else {
				go func() { _ = cmd.Wait() }()
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown action %q", verb)
	}
}

func (b *broker) notifyBreakpoint(reason string, bp obj) {
	if b.peer != nil && num(bp["id"]) > 0 {
		_ = b.peer.send(obj{"seq": 1000000001, "type": "event", "event": "breakpoint", "body": obj{"reason": reason, "breakpoint": obj{"id": bp["id"], "verified": true, "line": bp["line"], "source": obj{"path": bp["file"]}}}})
	}
}
