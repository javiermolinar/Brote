package broker

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"debug-handover/internal/agents/codex"
	"debug-handover/internal/delve"
	"debug-handover/internal/editors"
	"debug-handover/internal/editors/zed"
	"debug-handover/internal/session"
)

func (b *broker) action(a obj) (obj, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := a["generation"]; !ok || num(a["generation"]) != b.generation {
		return nil, fmt.Errorf("session changed; refresh state before acting")
	}
	verb := str(a["action"])
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
	if verb == "bind" {
		thread := str(a["thread"])
		if thread != "" && !codex.ValidThread(thread) {
			return nil, fmt.Errorf("thread must be a Codex task UUID")
		}
		var executable string
		if thread != "" {
			var err error
			executable, err = codex.Find()
			if err != nil {
				return nil, err
			}
		}
		if n := b.s.Notification; n != nil && (n.Status == "pending" || n.Status == "sending") {
			return nil, fmt.Errorf("wait for the pending notification before rebinding")
		}
		b.s.Thread, b.s.Codex = thread, executable
		b.generation++
		return obj{"thread": thread}, b.persist()
	}
	if verb == "retry-notification" {
		n := b.s.Notification
		if n == nil || (n.Status != "failed" && n.Status != "unknown") {
			return nil, fmt.Errorf("no failed notification to retry")
		}
		if (n.Kind == "handover" && b.owner != "zed") || (n.Kind == "reclaim" && b.owner != "codex") {
			return nil, fmt.Errorf("ownership changed; this notification is obsolete")
		}
		return obj{"message": "Retrying Codex notification"}, b.queueNotification(n.Kind)
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
		_ = b.persist()
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
		if !editors.IsOwner(b.owner) {
			return nil, fmt.Errorf("Codex already has control")
		}
		if status != "paused" {
			return nil, fmt.Errorf("pause in %s before giving control to Codex", editors.Name(b.owner))
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
		b.owner = "codex"
		b.generation++
		out := obj{"owner": b.owner, "status": "paused", "message": "Editor detached; the same debuggee remains paused"}
		if err := b.persist(); err != nil {
			out["persistenceError"] = err.Error()
		}
		if truth(a["notify"]) {
			if err := b.queueNotification("reclaim"); err != nil {
				out["notificationError"] = err.Error()
			}
		}
		return out, nil
	}
	if b.owner != "codex" && !(verb == "handover" && b.peer == nil && b.owner == "vscode") {
		return nil, fmt.Errorf("%s owns execution; reclaim the paused session first", editors.Name(b.owner))
	}
	if verb == "pause" {
		if status != "running" {
			return nil, fmt.Errorf("already paused")
		}
		if !truth(s["Running"]) {
			return nil, fmt.Errorf("execution command is starting or finishing; refresh and retry pause")
		}
		_, e = b.rpc("Command", obj{"name": "halt"})
		b.generation++
		return obj{"status": "pause requested"}, e
	}
	if status != "paused" {
		return nil, fmt.Errorf("pause the program before %s", verb)
	}
	if truth(s["NextInProgress"]) && verb != "continue" {
		return nil, fmt.Errorf("a step is still in progress; continue to complete it before %s", verb)
	}
	switch verb {
	case "continue", "next", "step", "stepout":
		name := verb
		if name == "stepout" {
			name = "stepOut"
		}
		b.moving = true
		b.lastError = ""
		b.generation++
		go func() {
			_, err := delve.Call(b.rpcAddr, "Command", obj{"name": name}, 0)
			b.mu.Lock()
			defer b.mu.Unlock()
			b.moving = false
			b.generation++
			if _, exited := delve.ExitState(err); err != nil && !exited {
				b.lastError = err.Error()
			}
		}()
		return obj{"status": "running", "command": verb}, nil
	case "break":
		bp := obj{"name": "codex" + session.NewID(4), "Cond": str(a["condition"]), "HitCond": str(a["hitCondition"])}
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
		}
		return out, e
	case "handover":
		editor := str(a["editor"])
		if editor == "" {
			editor = b.s.Editor
		}
		if editor == "" {
			editor = "zed"
		}
		if !editors.IsOwner(editor) {
			return nil, fmt.Errorf("editor must be zed or vscode")
		}
		if b.owner != "codex" && editor != b.owner {
			return nil, fmt.Errorf("reclaim before changing editors")
		}
		if editor == "vscode" {
			b.owner, b.s.Editor, b.s.HandoverID = editor, editor, session.NewID(8)
			b.lastError = ""
			b.generation++
			out := obj{"owner": editor, "status": "paused", "handoverId": b.s.HandoverID, "instructions": "The Debug Handover companion extension will attach in VS Code for this project."}
			if err := b.persist(); err != nil {
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
		if err := b.persist(); err != nil {
			out["persistenceError"] = err.Error()
		}
		if truth(a["notify"]) {
			if err := b.queueNotification("handover"); err != nil {
				out["notificationError"] = err.Error()
			} else {
				out["instructions"] = "Codex is being notified to attach Zed to the paused session."
			}
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
