package broker

import (
	"fmt"
	"io"
	"os"
	"strings"

	"agentdebugger/internal/editors/zed"
)

func (b *broker) snapshot(gid, frame int, brief bool) (obj, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked(gid, frame, brief)
}

func (b *broker) snapshotLocked(gid, frame int, brief bool) (obj, error) {
	s, e := b.state()
	if e != nil {
		return nil, e
	}
	status := stateStatus(s, b.moving)
	stateView := pick(s, "Pid", "Running", "NextInProgress", "exited", "exitStatus", "stopReason")
	stateView["currentThread"] = pick(asObj(s["currentThread"]), "id", "file", "line", "pc", "function", "goroutineID")
	stateView["currentGoroutine"] = pick(asObj(s["currentGoroutine"]), "id")
	v := obj{"id": b.s.ID, "owner": b.owner, "generation": b.generation, "status": status, "state": stateView, "zedConnected": b.owner == "zed" && b.peer != nil, "binary": b.s.Binary, "project": b.s.Project, "error": b.lastError, "dap": b.s.DAP, "label": zed.Label(b.s.ID)}
	protocol := "DAP"
	if b.s.Backend == "rpc" {
		protocol = "JSON-RPC"
	}
	v["debugger"] = obj{"adapter": "Delve", "protocol": protocol, "pid": b.s.DelvePID, "status": "connected", "mode": "launched"}
	v["historyError"] = b.historyError
	v["task"] = b.taskView()
	v["agentConnected"] = b.agentStreams > 0
	v["panel"] = b.s.HTTP + "/"
	v["version"], v["binding"], v["cursor"] = 2, b.s.Binding, b.s.Cursor
	v["capabilities"] = obj{"events": true, "browserOwner": true, "authentication": false, "comments": true, "replyContexts": true, "executionTasks": true, "taskStart": true}
	v["editor"], v["handoverId"] = b.s.Editor, b.s.HandoverID
	v["editorConnected"], v["editorReady"] = b.peer != nil, b.peer != nil && b.peer.ready
	v["vscodeConnected"] = b.owner == "vscode" && b.peer != nil
	v["thread"], v["watchExpressions"] = b.s.Thread, b.s.Watches
	if b.s.Notification != nil {
		n := *b.s.Notification
		v["notification"] = n
	}
	if status != "paused" || brief {
		return v, nil
	}
	if gid == 0 {
		gid = num(asObj(s["currentGoroutine"])["id"])
	}
	if gid == 0 {
		gid = -1
	}
	stack, stackErr := b.rpc("Stacktrace", obj{"Id": gid, "Depth": 30, "Full": false})
	if stackErr != nil {
		v["inspectionError"] = stackErr.Error()
	} else {
		frames := []any{}
		for _, f := range asList(stack["Locations"]) {
			frames = append(frames, pick(asObj(f), "file", "line", "pc", "function", "Err"))
		}
		v["frames"] = frames
	}
	gs, ge := b.rpc("ListGoroutines", obj{"Start": 0, "Count": 100})
	if ge == nil {
		goroutines := []any{}
		for _, g := range asList(gs["Goroutines"]) {
			goroutines = append(goroutines, pick(asObj(g), "id", "currentLoc", "userCurrentLoc"))
		}
		v["goroutines"] = goroutines
		v["nextGoroutine"] = gs["Nextg"]
	}
	bps, be := b.rpc("ListBreakpoints", obj{"All": false})
	if be == nil {
		breakpoints := []any{}
		for _, bp := range asList(bps["Breakpoints"]) {
			if num(asObj(bp)["id"]) > 0 {
				breakpoints = append(breakpoints, pick(asObj(bp), "id", "name", "file", "line", "functionName", "Cond", "HitCond", "totalHitCount", "disabled"))
			}
		}
		v["breakpoints"] = breakpoints
	}
	v["goroutine"] = gid
	frames := asList(v["frames"])
	if frame < 0 || frame >= len(frames) {
		frame = 0
	}
	v["frame"] = frame
	v["beforeGoStart"] = len(frames) == 0 && num(asObj(s["currentGoroutine"])["id"]) <= 0 && str(s["stopReason"]) == "entry"
	watches := []any{}
	for _, expression := range b.s.Watches {
		value, err := b.evaluate(expression, gid, frame, 2, 32, s)
		if err != nil {
			value = obj{"expression": expression, "error": err.Error()}
		}
		watches = append(watches, value)
	}
	v["watches"] = watches
	if len(frames) > 0 {
		f := asObj(frames[frame])
		scope := obj{"GoroutineID": gid, "Frame": frame}
		locals, le := b.rpc("ListLocalVars", obj{"Scope": scope, "Cfg": loadConfig})
		args, ae := b.rpc("ListFunctionArgs", obj{"Scope": scope, "Cfg": loadConfig})
		if le == nil {
			f["Locals"] = compactVariables(locals["Variables"], 0)
		}
		if ae == nil {
			f["Arguments"] = compactVariables(args["Args"], 0)
		}
		file := str(f["file"])
		line := num(f["line"])
		if data, e := readSource(file); e == nil {
			v["sourceIdentity"] = b.sourceIdentity(file, data)
			lines := strings.Split(string(data), "\n")
			start := max(1, line-12)
			end := min(len(lines), line+14)
			if start <= end {
				v["source"] = obj{"file": file, "line": line, "start": start, "lines": lines[start-1 : end]}
			}
			bs, e1 := os.Stat(b.s.Binary)
			ss, e2 := os.Stat(file)
			v["sourceNewerThanBinary"] = e1 == nil && e2 == nil && ss.ModTime().After(bs.ModTime())
		}
	}
	b.historyInspection(v)
	return v, nil
}

func readSource(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil || !s.Mode().IsRegular() || s.Size() > 2<<20 {
		return nil, fmt.Errorf("source unavailable or too large")
	}
	return io.ReadAll(f)
}
