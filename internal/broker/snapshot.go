package broker

import (
	"fmt"
	"io"
	"maps"
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
	stateView := pick(s, "Pid", "Running", "NextInProgress", "exited", "exitStatus", "stopReason", "stopDescription", "stopText")
	stateView["currentThread"] = pick(asObj(s["currentThread"]), "id", "file", "line", "pc", "function", "goroutineID")
	stateView["currentGoroutine"] = pick(asObj(s["currentGoroutine"]), "id")
	v := obj{"id": b.s.ID, "owner": b.owner, "generation": b.generation, "status": status, "state": stateView, "zedConnected": b.owner == "zed" && b.peer != nil, "binary": b.s.Binary, "project": b.s.Project, "error": b.lastError, "dap": b.s.DAP, "label": zed.Label(b.s.ID)}
	protocol := "DAP"
	if b.s.Backend == "rpc" {
		protocol = "JSON-RPC"
	}
	v["debugger"] = obj{"adapter": "Delve", "protocol": protocol, "pid": b.s.DelvePID, "status": "connected", "mode": "launched"}
	v["serviceVersion"], v["run"], v["pauseEpoch"] = b.s.ServiceVersion, b.s.RunID, b.handleEpoch
	if b.s.Attached {
		asObj(v["debugger"])["mode"] = "attached"
	}
	v["historyError"] = b.historyError
	v["traces"] = b.traces.Status()
	v["traceIds"], v["exportError"], v["exportFailures"] = b.s.TraceIDs, b.exportError, b.trace.Failures()
	v["stopAttribution"] = b.currentStop
	v["captureCounts"] = maps.Clone(b.s.CaptureCounts)
	v["capturePending"], v["captureCount"], v["captureSequence"] = b.capturePending, len(b.captures), b.s.CaptureSequence
	if len(b.captures) > 0 {
		v["lastCapture"] = b.captureView()[len(b.captures)-1].CaptureOutcome
	}
	if b.s.ServiceVersion > 0 {
		v["definitions"], v["resolutions"] = b.s.Definitions.Copy(), append([]definitionResolution{}, b.resolutions...)
	}
	v["task"] = b.taskView()
	v["agentConnected"] = b.agentStreams > 0
	v["panel"] = b.s.HTTP + "/"
	v["version"], v["binding"], v["cursor"] = 2, b.s.Binding, b.s.Cursor
	v["capabilities"] = obj{"events": true, "browserOwner": true, "authentication": b.s.ServiceVersion > 0, "comments": true, "replyContexts": true, "executionTasks": true, "taskStart": true}
	if b.s.ServiceVersion > 0 {
		asObj(v["capabilities"])["coordination"] = 1
		asObj(v["capabilities"])["service"] = obj{"version": 1, "definitions": true, "tracepoints": true, "captures": true, "otlp": b.trace != nil, "inspection": true, "restart": !b.s.Attached, "detach": b.s.Attached, "editorLimit": 1}
	}
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
	if b.s.ServiceVersion > 0 && b.backend != nil {
		return b.inspectSnapshot(v, s, gid, frame)
	}
	return b.enrichSnapshot(v, s, gid, frame)
}

func (b *broker) enrichSnapshot(v, s obj, gid, frame int) (obj, error) {
	if gid == 0 {
		gid = num(asObj(s["currentGoroutine"])["id"])
	}
	if gid == 0 {
		gid = -1
	}
	offset := 0
	if b.s.ServiceVersion > 0 && b.backend != nil && frame >= 30 {
		offset = (frame / 30) * 30
		frame -= offset
	}
	v["frameOffset"] = offset
	stack, stackErr := b.rpc("Stacktrace", obj{"Id": gid, "Start": offset, "Depth": 30, "Full": false})
	if stackErr != nil {
		v["inspectionError"] = stackErr.Error()
	} else {
		frames := []any{}
		for _, f := range asList(stack["Locations"]) {
			frames = append(frames, pick(asObj(f), "file", "line", "pc", "function", "Err"))
		}
		v["frames"] = frames
		if len(frames) > 0 && offset == 0 {
			asObj(v["state"])["currentThread"] = pick(asObj(frames[0]), "file", "line", "function")
		}
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
	if b.backend != nil && str(s["stopReason"]) == "exception" {
		details, err := b.rpc("ExceptionInfo", obj{"threadId": gid})
		if err != nil {
			v["exceptionError"] = err.Error()
		} else {
			v["exception"] = details
		}
	}
	frames := asList(v["frames"])
	if frame < 0 || frame >= len(frames) {
		frame = 0
	}
	v["frame"] = frame
	v["beforeGoStart"] = len(frames) == 0 && num(asObj(s["currentGoroutine"])["id"]) <= 0 && str(s["stopReason"]) == "entry"
	watches := []any{}
	for _, expression := range b.s.Watches {
		value, err := b.evaluate(expression, gid, frame+offset, 2, 32, s)
		if err != nil {
			value = obj{"expression": expression, "error": err.Error()}
		}
		watches = append(watches, value)
	}
	v["watches"] = watches
	if len(frames) > 0 {
		f := asObj(frames[frame])
		scope := obj{"GoroutineID": gid, "Frame": frame + offset}
		locals, le := b.rpc("ListLocalVars", obj{"Scope": scope, "Cfg": loadConfig})
		args, ae := b.rpc("ListFunctionArgs", obj{"Scope": scope, "Cfg": loadConfig})
		if le != nil {
			f["localsError"] = le.Error()
		}
		if truth(locals["truncated"]) {
			f["localsTruncated"] = true
		}
		if le == nil {
			f["Locals"] = compactVariables(locals["Variables"], 0)
		}
		if ae != nil {
			f["argumentsError"] = ae.Error()
		}
		if truth(args["truncated"]) {
			f["argumentsTruncated"] = true
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
