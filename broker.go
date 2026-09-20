package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html web/app.js web/style.css
var assets embed.FS

type Broker struct {
	mu         sync.Mutex
	s          Session
	rpcAddr    string
	owner      string
	generation int
	moving     bool
	lastError  string
	peer       *dapPeer
	done       chan struct{}
	once       sync.Once
}

func (b *Broker) rpc(method string, arg any) (obj, error) {
	return rpcCall(b.rpcAddr, method, arg, 5*time.Second)
}
func (b *Broker) state() (obj, error) {
	v, e := b.rpc("State", obj{"NonBlocking": true})
	if s, ok := exitState(e); ok {
		return s, nil
	}
	return asObj(v["State"]), e
}
func stateStatus(s obj, moving bool) string {
	if truth(s["exited"]) {
		return "exited"
	}
	if truth(s["Running"]) || moving {
		return "running"
	}
	return "paused"
}
func (b *Broker) snapshot(gid, frame int, brief bool) (obj, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, e := b.state()
	if e != nil {
		return nil, e
	}
	status := stateStatus(s, b.moving)
	stateView := pick(s, "Pid", "Running", "NextInProgress", "exited", "exitStatus", "stopReason")
	stateView["currentThread"] = pick(asObj(s["currentThread"]), "id", "file", "line", "pc", "function", "goroutineID")
	stateView["currentGoroutine"] = pick(asObj(s["currentGoroutine"]), "id")
	v := obj{"id": b.s.ID, "owner": b.owner, "generation": b.generation, "status": status, "state": stateView, "zedConnected": b.owner == "zed" && b.peer != nil, "binary": b.s.Binary, "project": b.s.Project, "error": b.lastError, "dap": b.s.DAP, "label": zedLabel(b.s.ID)}
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
		return nil, fail("source unavailable or too large")
	}
	return io.ReadAll(f)
}
func (b *Broker) action(a obj) (obj, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := a["generation"]; !ok || num(a["generation"]) != b.generation {
		return nil, fail("session changed; refresh state before acting")
	}
	verb := str(a["action"])
	if verb == "editor-error" {
		if b.owner != "vscode" || str(a["handoverId"]) != b.s.HandoverID || b.peer != nil {
			return nil, fail("editor handover changed")
		}
		b.lastError = "VS Code: " + str(a["error"])
		if len(b.lastError) > 1024 {
			b.lastError = b.lastError[:1024]
		}
		return obj{"error": b.lastError}, nil
	}
	if verb == "bind" {
		thread := str(a["thread"])
		if thread != "" && !validThread(thread) {
			return nil, fail("thread must be a Codex task UUID")
		}
		var executable string
		if thread != "" {
			var err error
			executable, err = findCodex()
			if err != nil {
				return nil, err
			}
		}
		if n := b.s.Notification; n != nil && (n.Status == "pending" || n.Status == "sending") {
			return nil, fail("wait for the pending notification before rebinding")
		}
		b.s.Thread, b.s.Codex = thread, executable
		b.generation++
		return obj{"thread": thread}, b.persist()
	}
	if verb == "retry-notification" {
		n := b.s.Notification
		if n == nil || (n.Status != "failed" && n.Status != "unknown") {
			return nil, fail("no failed notification to retry")
		}
		if (n.Kind == "handover" && b.owner != "zed") || (n.Kind == "reclaim" && b.owner != "codex") {
			return nil, fail("ownership changed; this notification is obsolete")
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
		if _, exited := exitState(e); e != nil && !exited {
			return nil, fail("could not stop Delve: %w", e)
		}
		b.s.Stopped = true
		_ = b.persist()
		cleanupErr := removeZedConfig(b.s)
		b.generation++
		go func() { time.Sleep(150 * time.Millisecond); b.once.Do(func() { close(b.done) }) }()
		return obj{"status": "terminated", "detachError": errorString(e), "cleanupError": errorString(cleanupErr)}, nil
	}
	if status == "exited" {
		return nil, fail("program exited; stop this session and start another")
	}
	if verb == "eval" || verb == "watch" || verb == "unwatch" {
		if status != "paused" || truth(s["NextInProgress"]) {
			return nil, fail("inspection requires a settled pause")
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
				return nil, fail("at most 16 watches are supported")
			}
			list = append(list, expression)
		}
		b.s.Watches = list
		b.generation++
		return obj{"watches": list}, b.persist()
	}
	if verb == "reclaim" {
		if !editorOwner(b.owner) {
			return nil, fail("Codex already has control")
		}
		if status != "paused" {
			return nil, fail("pause in %s before giving control to Codex", editorName(b.owner))
		}
		if truth(s["NextInProgress"]) {
			return nil, fail("a step is still in progress; settle it in the editor before handback")
		}
		if b.peer != nil {
			if b.peer.pendingCount() > 0 {
				return nil, fail("editor has requests in flight; wait for the pause to settle")
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
		return nil, fail("%s owns execution; reclaim the paused session first", editorName(b.owner))
	}
	if verb == "pause" {
		if status != "running" {
			return nil, fail("already paused")
		}
		if !truth(s["Running"]) {
			return nil, fail("execution command is starting or finishing; refresh and retry pause")
		}
		_, e = b.rpc("Command", obj{"name": "halt"})
		b.generation++
		return obj{"status": "pause requested"}, e
	}
	if status != "paused" {
		return nil, fail("pause the program before %s", verb)
	}
	if truth(s["NextInProgress"]) && verb != "continue" {
		return nil, fail("a step is still in progress; continue to complete it before %s", verb)
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
			_, err := rpcCall(b.rpcAddr, "Command", obj{"name": name}, 0)
			b.mu.Lock()
			defer b.mu.Unlock()
			b.moving = false
			b.generation++
			if _, exited := exitState(err); err != nil && !exited {
				b.lastError = err.Error()
			}
		}()
		return obj{"status": "running", "command": verb}, nil
	case "break":
		bp := obj{"name": "codex" + randomID(4), "Cond": str(a["condition"]), "HitCond": str(a["hitCondition"])}
		loc := str(a["function"])
		if loc == "" {
			file := str(a["file"])
			if file == "" || num(a["line"]) < 1 {
				return nil, fail("provide a file and positive line, or a function")
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
			return nil, fail("positive breakpoint ID required")
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
		if !editorOwner(editor) {
			return nil, fail("editor must be zed or vscode")
		}
		if b.owner != "codex" && editor != b.owner {
			return nil, fail("reclaim before changing editors")
		}
		if editor == "vscode" {
			b.owner, b.s.Editor, b.s.HandoverID = editor, editor, randomID(8)
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
		path, e := writeZedConfig(b.s)
		if e != nil {
			return nil, e
		}
		b.owner = "zed"
		b.s.Editor, b.s.HandoverID = "zed", randomID(8)
		b.lastError = ""
		b.generation++
		out := obj{"owner": "zed", "status": "paused", "config": path, "label": zedLabel(b.s.ID), "instructions": "In Zed, press F4 and choose " + zedLabel(b.s.ID)}
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
		return nil, fail("unknown action %q", verb)
	}
}
func errorString(e error) string {
	if e != nil {
		return e.Error()
	}
	return ""
}
func (b *Broker) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		write := func(code int, v any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(v) }
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.s.Token)) != 1 {
			write(401, obj{"error": "session token required"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != b.s.HTTP {
			write(403, obj{"error": "foreign origin rejected"})
			return
		}
		var v obj
		var e error
		switch {
		case r.URL.Path == "/api/state" && r.Method == "GET":
			gid, _ := strconv.Atoi(r.URL.Query().Get("goroutine"))
			frame, _ := strconv.Atoi(r.URL.Query().Get("frame"))
			v, e = b.snapshot(gid, frame, r.URL.Query().Get("brief") == "1")
		case r.URL.Path == "/api/action" && r.Method == "POST":
			var a obj
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
			if e = dec.Decode(&a); e == nil {
				v, e = b.action(a)
			}
		default:
			write(404, obj{"error": "unknown endpoint"})
			return
		}
		if e != nil {
			write(409, obj{"error": e.Error()})
			return
		}
		write(200, v)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if name != "index.html" && name != "app.js" && name != "style.css" {
			http.NotFound(w, r)
			return
		}
		data, e := assets.ReadFile("web/" + name)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		switch filepath.Ext(name) {
		case ".js":
			w.Header().Set("Content-Type", "text/javascript")
		case ".css":
			w.Header().Set("Content-Type", "text/css")
		default:
			w.Header().Set("Content-Type", "text/html")
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if "http://"+r.Host != b.s.HTTP {
			http.Error(w, "unexpected host", 403)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'self'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}
