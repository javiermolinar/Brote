package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Session struct {
	ID           string        `json:"id"`
	PID          int           `json:"brokerPid"`
	Binary       string        `json:"binary"`
	Project      string        `json:"project"`
	HTTP         string        `json:"http"`
	DAP          string        `json:"dap"`
	Token        string        `json:"token"`
	Dir          string        `json:"directory"`
	Created      string        `json:"created"`
	RPC          string        `json:"rpc,omitempty"`
	DelvePID     int           `json:"delvePid,omitempty"`
	TargetPID    int           `json:"targetPid,omitempty"`
	Thread       string        `json:"thread,omitempty"`
	Codex        string        `json:"codex,omitempty"`
	Owner        string        `json:"owner,omitempty"`
	Editor       string        `json:"editor,omitempty"`
	HandoverID   string        `json:"handoverId,omitempty"`
	Watches      []string      `json:"watches,omitempty"`
	Fingerprint  *Fingerprint  `json:"fingerprint,omitempty"`
	Notification *Notification `json:"notification,omitempty"`
	Stopped      bool          `json:"stopped,omitempty"`
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func sessionRoot() string {
	if p := os.Getenv("DEBUG_HANDOVER_HOME"); p != "" {
		return p
	}
	p, e := os.UserCacheDir()
	if e != nil {
		panic(e)
	}
	return filepath.Join(p, "debug-handover", "sessions")
}
func readSession(id string) (Session, error) {
	var s Session
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return s, fail("invalid session ID")
	}
	b, e := os.ReadFile(filepath.Join(sessionRoot(), id, "session.json"))
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(b, &s)
	return s, e
}
func api(s Session, method, path string, body any) (obj, error) {
	var r io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		r = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, s.HTTP+path, r)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	v := obj{}
	if e = json.NewDecoder(resp.Body).Decode(&v); e != nil {
		return nil, e
	}
	if resp.StatusCode >= 400 {
		return v, fail("%s", str(v["error"]))
	}
	return v, nil
}
func start(args []string) (obj, error) {
	f := flag.NewFlagSet("start", flag.ContinueOnError)
	bin := f.String("binary", "", "Existing Go executable or test binary")
	project := f.String("project", ".", "Project/source directory")
	dlv := f.String("dlv", "dlv", "Delve executable")
	thread := f.String("thread", os.Getenv("CODEX_THREAD_ID"), "Codex task for inspector handovers; empty disables wakeups")
	if e := f.Parse(args); e != nil {
		return nil, e
	}
	if *bin == "" {
		return nil, fail("--binary is required; build once with go build -gcflags='all=-N -l'")
	}
	if *thread != "" && !validThread(*thread) {
		return nil, fail("--thread must be a Codex task UUID")
	}
	abs, e := filepath.Abs(*bin)
	if e != nil {
		return nil, e
	}
	st, e := os.Stat(abs)
	if e != nil {
		return nil, e
	}
	if st.IsDir() || st.Mode()&0111 == 0 {
		return nil, fail("binary is not executable: %s", abs)
	}
	root, e := filepath.Abs(*project)
	if e != nil {
		return nil, e
	}
	st, e = os.Stat(root)
	if e != nil || !st.IsDir() {
		return nil, fail("project directory does not exist: %s", root)
	}
	delve, e := exec.LookPath(*dlv)
	if e != nil && *dlv == "dlv" {
		home, _ := os.UserHomeDir()
		delve, e = exec.LookPath(filepath.Join(home, "go", "bin", "dlv"))
	}
	if e != nil {
		return nil, e
	}
	id := randomID(5)
	dir := filepath.Join(sessionRoot(), id)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	log, e := os.OpenFile(filepath.Join(dir, "broker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return nil, e
	}
	defer log.Close()
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	childArgs := []string{"serve", "--id", id, "--binary", abs, "--project", root, "--dlv", delve, "--thread", *thread, "--"}
	childArgs = append(childArgs, f.Args()...)
	cmd := exec.Command(exe, childArgs...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	go func() { _ = cmd.Wait() }()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		s, e := readSession(id)
		if e == nil {
			return obj{"id": id, "panel": s.HTTP + "/#" + s.Token, "binary": abs, "project": root, "status": "paused at launch", "log": filepath.Join(dir, "delve.log")}, nil
		}
		if data, e := os.ReadFile(filepath.Join(dir, "error")); e == nil {
			return nil, fail("start failed: %s", data)
		}
	}
	_ = cmd.Process.Kill()
	return nil, fail("start timed out; inspect %s", filepath.Join(dir, "broker.log"))
}
func usage() string {
	return `Debug Handover — persistent Go / Delve sessions

  debug-handover start --binary PATH --project DIR [--dlv PATH] -- [program args]
  debug-handover sessions
  debug-handover state ID [--goroutine N] [--frame N]
  debug-handover eval ID --expression EXPR [--goroutine N] [--frame N] [--depth 3] [--count 64]
  debug-handover watch|unwatch ID --expression EXPR
  debug-handover bind ID --thread UUID
  debug-handover retry-notification ID
  debug-handover recover ID
  debug-handover cleanup ID
  debug-handover doctor [--binary PATH] [--project DIR]
  debug-handover break ID --file PATH --line N [--condition EXPR] [--hit-condition '== 3']
  debug-handover break ID --function main.process [--condition EXPR]
  debug-handover clear ID --breakpoint N
  debug-handover continue|next|step|stepout ID [--wait 20s]
  debug-handover pause ID
  debug-handover handover ID [--editor zed|vscode] [--no-open]
  debug-handover reclaim ID
  debug-handover stop ID

All commands print JSON. start never compiles the target. Closing Zed or the panel
does not stop the program. stop explicitly terminates the owned debug session.`
}
func run(args []string) (any, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println(usage())
		return nil, nil
	}
	verb := args[0]
	if verb == "start" {
		return start(args[1:])
	}
	if verb == "serve" {
		return nil, serve(args[1:])
	}
	if verb == "doctor" {
		return doctor(args[1:])
	}
	if (verb == "recover" || verb == "cleanup") && len(args) == 2 {
		if verb == "recover" {
			return recoverSession(args[1])
		}
		return cleanupSession(args[1])
	}
	if verb == "sessions" {
		entries, _ := os.ReadDir(sessionRoot())
		list := []any{}
		for _, ent := range entries {
			s, e := readSession(ent.Name())
			if e != nil {
				continue
			}
			if s.Stopped {
				list = append(list, obj{"id": s.ID, "binary": s.Binary, "project": s.Project, "status": "ended"})
				continue
			}
			v, e := api(s, "GET", "/api/state?brief=1", nil)
			if e != nil {
				list = append(list, obj{"id": s.ID, "binary": s.Binary, "project": s.Project, "status": "offline", "recoverable": s.RPC != "" && !s.Stopped, "error": e.Error()})
				continue
			}
			list = append(list, obj{"id": s.ID, "binary": s.Binary, "project": s.Project, "owner": v["owner"], "status": v["status"], "panel": s.HTTP + "/#" + s.Token})
		}
		return list, nil
	}
	if len(args) < 2 {
		return nil, fail("session ID required\n%s", usage())
	}
	s, e := readSession(args[1])
	if e != nil {
		return nil, e
	}
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	file := f.String("file", "", "source path")
	line := f.Int("line", 0, "line")
	fn := f.String("function", "", "function name")
	cond := f.String("condition", "", "condition")
	hit := f.String("hit-condition", "", "hit condition")
	bp := f.Int("breakpoint", 0, "breakpoint ID")
	gid := f.Int("goroutine", 0, "goroutine ID")
	frame := f.Int("frame", 0, "frame index")
	wait := f.Duration("wait", 0, "wait for pause")
	noOpen := f.Bool("no-open", false, "do not open the editor")
	editor := f.String("editor", "", "handover editor: zed or vscode (defaults to previous editor)")
	expr := f.String("expression", "", "read-only Go expression")
	depth := f.Int("depth", 3, "variable depth (0–6)")
	count := f.Int("count", 64, "maximum array/struct entries (1–128)")
	thread := f.String("thread", "", "Codex task UUID; empty disables wakeups")
	notify := f.Bool("notify", false, "queue a handover message to the bound Codex task")
	if e = f.Parse(args[2:]); e != nil {
		return nil, e
	}
	if len(f.Args()) > 0 {
		return nil, fail("unexpected arguments: %v", f.Args())
	}
	if verb == "state" {
		return api(s, "GET", fmt.Sprintf("/api/state?goroutine=%d&frame=%d", *gid, *frame), nil)
	}
	state, e := api(s, "GET", "/api/state?brief=1", nil)
	if e != nil {
		return nil, e
	}
	body := obj{"action": verb, "generation": state["generation"], "file": *file, "line": *line, "function": *fn, "condition": *cond, "hitCondition": *hit, "breakpoint": *bp, "open": !*noOpen}
	body["editor"] = *editor
	body["expression"], body["depth"], body["count"], body["goroutine"], body["frame"], body["thread"], body["notify"] = *expr, *depth, *count, *gid, *frame, *thread, *notify
	res, e := api(s, "POST", "/api/action", body)
	if e != nil {
		return nil, e
	}
	if *wait > 0 {
		for deadline := time.Now().Add(*wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			v, e := api(s, "GET", "/api/state", nil)
			if e != nil {
				return nil, e
			}
			if str(v["status"]) != "running" {
				return v, nil
			}
		}
		return nil, fail("wait timed out; session remains alive (use state or pause)")
	}
	return res, nil
}
func main() {
	v, e := run(os.Args[1:])
	if e != nil {
		fmt.Fprintln(os.Stderr, pretty(obj{"error": e.Error()}))
		os.Exit(1)
	}
	if v != nil {
		fmt.Println(pretty(v))
	}
}
