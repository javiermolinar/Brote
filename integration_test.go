package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type testDAP struct {
	t      *testing.T
	c      net.Conn
	r      *bufio.Reader
	seq    int
	events []obj
}

func (d *testDAP) receive() obj {
	d.t.Helper()
	_ = d.c.SetReadDeadline(time.Now().Add(10 * time.Second))
	v, e := readDAP(d.r)
	if e != nil {
		d.t.Fatal(e)
	}
	return v
}
func (d *testDAP) request(command string, args obj) obj {
	d.t.Helper()
	d.seq++
	if e := writeDAP(d.c, obj{"seq": d.seq, "type": "request", "command": command, "arguments": args}); e != nil {
		d.t.Fatal(e)
	}
	for {
		v := d.receive()
		if str(v["type"]) == "event" {
			d.events = append(d.events, v)
			continue
		}
		if num(v["request_seq"]) != d.seq {
			d.t.Fatalf("unexpected reply %v", v)
		}
		return v
	}
}
func (d *testDAP) ok(command string, args obj) obj {
	d.t.Helper()
	v := d.request(command, args)
	if !truth(v["success"]) {
		d.t.Fatalf("%s: %v", command, v)
	}
	return asObj(v["body"])
}
func (d *testDAP) event(name string) obj {
	d.t.Helper()
	for {
		for i, v := range d.events {
			if str(v["event"]) == name {
				d.events = append(d.events[:i], d.events[i+1:]...)
				return asObj(v["body"])
			}
		}
		d.events = append(d.events, d.receive())
	}
}
func newTestDAP(t *testing.T, s Session) *testDAP {
	t.Helper()
	c, e := net.Dial("tcp", s.DAP)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	d := &testDAP{t: t, c: c, r: bufio.NewReader(c)}
	d.ok("initialize", obj{"adapterID": "go", "clientID": "handover-test", "pathFormat": "path", "linesStartAt1": true, "columnsStartAt1": true, "supportsVariableType": true})
	d.ok("attach", obj{"mode": "remote", "stopOnEntry": false})
	d.event("initialized")
	return d
}
func TestIntegrationRoundTrip(t *testing.T) {
	if os.Getenv("DH_INTEGRATION") != "1" {
		t.Skip("set DH_INTEGRATION=1 to test real Delve")
	}
	for _, mode := range []string{"main-goroutine", "worker-goroutine"} {
		t.Run(mode, func(t *testing.T) { testRoundTrip(t, mode == "worker-goroutine") })
	}
}

func testRoundTrip(t *testing.T, worker bool) {
	dir := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(dir, "sessions"))
	t.Setenv("CODEX_THREAD_ID", "")
	helper := filepath.Join(dir, "debug-handover")
	cmd := exec.Command("go", "build", "-race", "-o", helper, ".")
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build helper: %s: %v", out, e)
	}
	project := filepath.Join(dir, "demo")
	if e := os.Mkdir(project, 0755); e != nil {
		t.Fatal(e)
	}
	source, e := os.ReadFile("examples/demo/main.go")
	if e != nil {
		t.Fatal(e)
	}
	if worker {
		source = []byte(strings.Replace(string(source), "func main() {", "func main() { done := make(chan struct{}); go func() { worker(); close(done) }(); <-done }\n\nfunc worker() {", 1))
	}
	source = []byte(strings.Replace(string(source), "func process(attempt, total int) int {", "func process(attempt, total int) int {\n samples := []struct { Name string; Values []int }{{\"first\", []int{1,2,3}}, {\"second\", []int{4,5,6}}}; _ = samples", 1))
	sourceLine := func(marker string) int {
		t.Helper()
		for i, line := range strings.Split(string(source), "\n") {
			if strings.Contains(line, marker) {
				return i + 1
			}
		}
		t.Fatalf("missing fixture marker %q", marker)
		return 0
	}
	breakLine := sourceLine("// HANDOVER:")
	stepLine := sourceLine("fmt.Printf")
	returnLine := sourceLine("return total")
	file := filepath.Join(project, "main.go")
	if e = os.WriteFile(file, source, 0644); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(project, "demo")
	cmd = exec.Command("go", "build", "-gcflags=all=-N -l", "-o", binary, file)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build fixture: %s: %v", out, e)
	}
	before, _ := os.ReadFile(binary)
	digest := sha256.Sum256(before)
	cmd = exec.Command(helper, "start", "--binary", binary, "--project", project)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("start: %s: %v", out, e)
	}
	var result obj
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e)
	}
	s, e := readSession(str(result["id"]))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		v, e := api(s, "GET", "/api/state?brief=1", nil)
		if e == nil {
			_, _ = api(s, "POST", "/api/action", obj{"action": "stop", "generation": v["generation"]})
		}
		time.Sleep(300 * time.Millisecond)
		log, _ := os.ReadFile(filepath.Join(s.Dir, "broker.log"))
		if strings.Contains(string(log), "DATA RACE") {
			t.Errorf("broker race: %s", log)
		}
	})
	state := func() obj {
		t.Helper()
		v, e := api(s, "GET", "/api/state", nil)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	action := func(name string, a obj) obj {
		t.Helper()
		v := state()
		if a == nil {
			a = obj{}
		}
		a["action"] = name
		a["generation"] = v["generation"]
		r, e := api(s, "POST", "/api/action", a)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		return r
	}
	waitPause := func() obj {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
			v := state()
			if str(v["status"]) == "paused" {
				return v
			}
		}
		t.Fatal("pause timed out")
		return nil
	}
	line := breakLine
	functionBP := action("break", obj{"function": "main.process"})
	action("clear", obj{"breakpoint": asObj(functionBP["Breakpoint"])["id"]})
	action("break", obj{"file": "main.go", "line": line, "condition": "attempt == 3", "hitCondition": "== 1"})
	action("continue", nil)
	paused := waitPause()
	pid := num(asObj(paused["state"])["Pid"])
	gid := num(asObj(asObj(paused["state"])["currentGoroutine"])["id"])
	if worker && gid == 1 {
		t.Fatal("fixture did not stop in a worker goroutine")
	}
	check := func(s obj, wantLine int, wantTotal string) {
		t.Helper()
		if num(asObj(s["state"])["Pid"]) != pid {
			t.Fatal("process was replaced")
		}
		frames := asList(s["frames"])
		if len(frames) == 0 || num(asObj(frames[0])["line"]) != wantLine {
			t.Fatalf("wrong stop: %s", pretty(s["state"]))
		}
		vars := asList(asObj(frames[0])["Arguments"])
		found := false
		for _, v := range vars {
			if str(asObj(v)["name"]) == "total" {
				found = true
				if str(asObj(v)["value"]) != wantTotal {
					t.Fatalf("total: %v", v)
				}
			}
		}
		if !found {
			t.Fatal("total missing")
		}
	}
	check(paused, line, "21")
	evaluated := action("eval", obj{"expression": "total + delta", "depth": 3, "count": 64})
	if str(asObj(evaluated["value"])["value"]) != "42" {
		t.Fatalf("bad expression value: %v", evaluated)
	}
	evaluated = action("eval", obj{"expression": "samples", "depth": 6, "count": 1})
	if num(asObj(evaluated["value"])["len"]) != 2 || len(asList(asObj(evaluated["value"])["children"])) != 1 {
		t.Fatalf("bounded deep inspection: %s", pretty(evaluated))
	}
	if _, e = api(s, "POST", "/api/action", obj{"action": "eval", "generation": state()["generation"], "expression": "process(1, 2)", "depth": 3, "count": 64}); e == nil {
		t.Fatal("function call accepted")
	}
	action("watch", obj{"expression": "total"})
	if _, err := api(s, "POST", "/api/action", obj{"action": "handover", "generation": state()["generation"], "editor": "unknown"}); err == nil {
		t.Fatal("invalid editor accepted")
	}
	editor := "zed"
	if worker {
		editor = "vscode"
	}
	hand := action("handover", obj{"open": false, "editor": editor})
	if str(hand["owner"]) != editor {
		t.Fatalf("wrong editor: %v", hand)
	}
	if worker {
		if hasZedProfile(s) {
			t.Fatal("VS Code handover wrote a Zed profile")
		}
		retry := action("handover", obj{"open": false, "editor": editor})
		if str(retry["handoverId"]) == str(hand["handoverId"]) {
			t.Fatal("retry did not create a fresh handover")
		}
		if _, err := api(s, "POST", "/api/action", obj{"action": "editor-error", "generation": state()["generation"], "handoverId": hand["handoverId"], "error": "stale"}); err == nil {
			t.Fatal("stale extension report accepted")
		}
	}
	d := newTestDAP(t, s)
	// A source breakpoint set from Zed must not erase the conditional Codex breakpoint.
	r := d.ok("setBreakpoints", obj{"source": obj{"path": file}, "breakpoints": []any{obj{"line": returnLine}}})
	if !truth(asObj(asList(r["breakpoints"])[0])["verified"]) {
		t.Fatal(r)
	}
	d.ok("configurationDone", obj{})
	if event := d.event("stopped"); num(event["threadId"]) != gid {
		t.Fatalf("attach focused wrong goroutine: want %d, event=%v", gid, event)
	}
	attached := waitPause()
	if !truth(attached["editorReady"]) || str(attached["owner"]) != editor {
		t.Fatalf("editor did not finish attaching: %v", attached)
	}
	evaluated = action("eval", obj{"expression": "total", "depth": 3, "count": 64})
	if str(asObj(evaluated["value"])["value"]) != "21" {
		t.Fatal("observer evaluation failed")
	}
	check(attached, line, "21")
	count := 0
	for _, bp := range asList(attached["breakpoints"]) {
		if num(asObj(bp)["id"]) > 0 {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("breakpoints not preserved: %v", attached["breakpoints"])
	}
	if _, e = api(s, "POST", "/api/action", obj{"action": "next", "generation": attached["generation"]}); e == nil || !strings.Contains(e.Error(), editorName(editor)+" owns") {
		t.Fatalf("Codex executed during Zed ownership: %v", e)
	}
	d.ok("next", obj{"threadId": gid})
	d.event("stopped")
	stepped := waitPause()
	check(stepped, stepLine, "42")
	action("reclaim", nil)
	d.event("terminated")
	returned := state()
	check(returned, stepLine, "42")
	if str(returned["owner"]) != "codex" || truth(returned["editorConnected"]) {
		t.Fatal("ownership did not return")
	}
	if str(asObj(asObj(asList(returned["watches"])[0])["value"])["value"]) != "42" {
		t.Fatal("watch did not refresh after stepping")
	}
	// Kill only the broker. Delve and its paused target must survive, and the
	// replacement broker must retain watches, breakpoints, token, and stop PC.
	pc := asObj(asObj(returned["state"])["currentThread"])["pc"]
	var recoveryHandover string
	if worker {
		recoveryHandover = str(action("handover", obj{"open": false})["handoverId"])
	}
	if e = syscall.Kill(s.PID, syscall.SIGKILL); e != nil {
		t.Fatal(e)
	}
	var occupied net.Listener
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		occupied, e = net.Listen("tcp", s.DAP)
		if e == nil {
			break
		}
	}
	if e != nil {
		t.Fatal("broker did not release listener", e)
	}
	if worker {
		occupied.Close()
		occupied = nil
	} else {
		defer occupied.Close()
	}
	old := s
	cmd = exec.Command(helper, "recover", s.ID)
	if out, e = cmd.CombinedOutput(); e != nil {
		t.Fatalf("recover: %s: %v", out, e)
	}
	s, e = readSession(s.ID)
	if e != nil {
		t.Fatal(e)
	}
	if s.Token != old.Token || s.HTTP != old.HTTP || s.TargetPID != pid {
		t.Fatal("recovery lost identity or stable HTTP endpoint")
	}
	if !worker && s.DAP == old.DAP {
		t.Fatal("occupied DAP port was not rebound")
	}
	recovered := state()
	if worker {
		if str(recovered["owner"]) != "vscode" || str(recovered["handoverId"]) == recoveryHandover {
			t.Fatal("recovery lost VS Code ownership or did not refresh the attach request")
		}
		action("reclaim", nil)
	}
	check(recovered, stepLine, "42")
	if asObj(asObj(recovered["state"])["currentThread"])["pc"] != pc || len(asList(recovered["watches"])) != 1 {
		t.Fatal("recovery changed stop or lost watches")
	}
	if _, e = api(s, "POST", "/api/action", obj{"action": "next", "generation": returned["generation"]}); e == nil {
		t.Fatal("pre-crash action was accepted")
	}
	// The same source-level Zed breakpoint must still work under RPC control.
	action("continue", nil)
	atZedBreakpoint := waitPause()
	check(atZedBreakpoint, returnLine, "42")
	action("handover", obj{"open": false})
	d2 := newTestDAP(t, s)
	d2.ok("configurationDone", obj{})
	if event := d2.event("stopped"); num(event["threadId"]) != gid {
		t.Fatalf("reattach focused wrong goroutine: want %d, event=%v", gid, event)
	}
	check(waitPause(), returnLine, "42")
	// Even a frontend asking to terminate on disconnect cannot kill this owned session.
	d2.ok("disconnect", obj{"terminateDebuggee": true})
	d2.c.Close()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !truth(state()["editorConnected"]) {
			break
		}
	}
	action("reclaim", nil)
	check(state(), returnLine, "42")
	after, _ := os.ReadFile(binary)
	if sha256.Sum256(after) != digest {
		t.Fatal("target binary changed during session")
	}
	for _, bp := range asList(state()["breakpoints"]) {
		action("clear", obj{"breakpoint": asObj(bp)["id"]})
	}
	action("continue", nil)
	exited := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if str(state()["status"]) == "exited" {
			exited = true
			break
		}
	}
	if !exited {
		t.Fatal("natural program exit did not settle")
	}
	action("stop", nil)
	if hasZedProfile(s) {
		t.Fatal("stop left the generated profile behind")
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && processExists(s.DelvePID); time.Sleep(30 * time.Millisecond) {
	}
	if processExists(s.DelvePID) {
		t.Fatal("stop left a recovered Delve process alive")
	}
	t.Logf("PASS: PID %d retained across RPC → DAP → RPC → DAP, total 21 → 42, both breakpoint sets preserved, binary unchanged", pid)
}
