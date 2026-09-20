package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func writePrivateJSON(path string, value any) error {
	data, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".handover-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
func (b *Broker) persist() error {
	b.s.Owner = b.owner
	return writePrivateJSON(filepath.Join(b.s.Dir, "session.json"), b.s)
}
func lockSession(dir string) (*os.File, error) {
	f, e := os.OpenFile(filepath.Join(dir, "broker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fail("session already has a broker or maintenance operation")
	}
	return f, nil
}
func unlockSession(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }
func listenSession(previous string) (net.Listener, error) {
	if previous != "" {
		if ln, e := net.Listen("tcp", previous); e == nil {
			return ln, nil
		}
	}
	return net.Listen("tcp", "127.0.0.1:0")
}
func validateLoopback(address string) error {
	host, _, e := net.SplitHostPort(address)
	if e != nil || host != "127.0.0.1" {
		return fail("session endpoint must use 127.0.0.1")
	}
	return nil
}
func serve(args []string) (err error) {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	id := f.String("id", "", "session")
	bin := f.String("binary", "", "binary")
	project := f.String("project", "", "project")
	dlv := f.String("dlv", "dlv", "Delve")
	recovering := f.Bool("recover", false, "reattach broker")
	thread := f.String("thread", "", "Codex task")
	if e := f.Parse(args); e != nil {
		return e
	}
	dir := filepath.Join(sessionRoot(), *id)
	lock, e := lockSession(dir)
	if e != nil {
		return e
	}
	defer unlockSession(lock)
	defer func() {
		if err != nil {
			_ = os.WriteFile(filepath.Join(dir, "error"), []byte(err.Error()), 0600)
		}
	}()
	s := Session{ID: *id, PID: os.Getpid(), Binary: *bin, Project: *project, Dir: dir, Token: randomID(32), Created: time.Now().Format(time.RFC3339), Owner: "codex", Thread: *thread}
	var process *exec.Cmd
	committed := false
	defer func() {
		if process != nil && !committed {
			_ = process.Process.Kill()
			_ = process.Wait()
		}
	}()
	if *recovering {
		s, e = readSession(*id)
		if e != nil {
			return e
		}
		if s.Stopped {
			return fail("session was explicitly stopped")
		}
		if s.RPC == "" || s.TargetPID == 0 {
			return fail("this older session has no recovery metadata; it cannot be restarted safely")
		}
		if e = validateLoopback(s.RPC); e != nil {
			return e
		}
		s.PID = os.Getpid()
		// A send interrupted by a broker crash has ambiguous delivery. Keep it visible
		// and require an explicit retry instead of replaying an event automatically.
		if n := s.Notification; n != nil && (n.Status == "pending" || n.Status == "sending") {
			n.Status = "unknown"
			n.Error = "broker stopped during delivery; check the Codex task before retrying"
		}
	} else {
		if s.Thread != "" {
			if !validThread(s.Thread) {
				return fail("invalid task UUID")
			}
			s.Codex, e = findCodex()
			if e != nil {
				return e
			}
		}
		log, e := os.OpenFile(filepath.Join(dir, "delve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return e
		}
		defer log.Close()
		argv := append([]string{"exec", s.Binary, "--headless", "--listen=127.0.0.1:0", "--api-version=2", "--accept-multiclient", "--"}, f.Args()...)
		process = exec.Command(*dlv, argv...)
		process.Dir = s.Project
		process.Stdout = log
		process.Stderr = log
		// Delve has its own session and a file-backed log, so a broker crash does not
		// sever its stdout pipe or deliver a terminal signal to the target.
		process.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if e = process.Start(); e != nil {
			return e
		}
		s.DelvePID = process.Process.Pid
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			data, _ := os.ReadFile(filepath.Join(dir, "delve.log"))
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "API server listening at: ") {
					s.RPC = strings.TrimSpace(strings.TrimPrefix(line, "API server listening at: "))
					break
				}
			}
			if s.RPC != "" {
				break
			}
		}
		if s.RPC == "" {
			return fail("Delve did not start; see %s", filepath.Join(dir, "delve.log"))
		}
		s.Fingerprint = fingerprint(s.Binary, s.Project)
	}
	b := &Broker{s: s, rpcAddr: s.RPC, owner: s.Owner, generation: int(time.Now().UnixMilli()), done: make(chan struct{})}
	if b.owner == "" {
		b.owner = "codex"
	}
	state, e := b.state()
	if e != nil {
		return fail("Delve is unavailable; debuggee was not relaunched: %w", e)
	}
	if *recovering && num(state["Pid"]) != s.TargetPID {
		return fail("Delve process identity differs; refusing recovery")
	}
	b.s.TargetPID = num(state["Pid"])
	if *recovering && b.owner == "vscode" {
		b.s.HandoverID = randomID(8)
	}
	if !*recovering {
		b.captureSources()
	}
	if b.s.DAP != "" {
		if e = validateLoopback(b.s.DAP); e != nil {
			return e
		}
	}
	dap, e := listenSession(b.s.DAP)
	if e != nil {
		return e
	}
	defer dap.Close()
	httpAddr := strings.TrimPrefix(b.s.HTTP, "http://")
	if httpAddr != "" {
		if e = validateLoopback(httpAddr); e != nil {
			return e
		}
	}
	httpLn, e := listenSession(httpAddr)
	if e != nil {
		return e
	}
	defer httpLn.Close()
	b.s.DAP = dap.Addr().String()
	b.s.HTTP = "http://" + httpLn.Addr().String()
	// Only refresh a profile already created for this session. Starting a session
	// does not need to alter the user's Zed configuration.
	if *recovering && hasZedProfile(b.s) {
		if _, e = writeZedConfig(b.s); e != nil {
			return e
		}
	}
	server := &http.Server{Handler: b.handler(), ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	if e = b.persist(); e != nil {
		return e
	}
	committed = true
	go func() { _ = server.Serve(httpLn) }()
	go b.acceptDAP(dap)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sig)
	select {
	case <-b.done:
	case <-sig:
	}
	b.mu.Lock()
	if b.peer != nil {
		b.peer.close()
		b.peer = nil
	}
	// Deliberate broker shutdown leaves a recoverable target paused. A hard crash
	// may leave it running; recover reports that state rather than silently halting.
	if !b.s.Stopped {
		if current, e := b.state(); e == nil && truth(current["Running"]) {
			_, _ = b.rpc("Command", obj{"name": "halt"})
		}
	}
	_ = b.persist()
	stopped := b.s.Stopped
	b.mu.Unlock()
	if process != nil && stopped {
		_ = process.Process.Kill()
		_ = process.Wait()
	}
	return nil
}
func recoverSession(id string) (obj, error) {
	s, e := readSession(id)
	if e != nil {
		return nil, e
	}
	if _, e = api(s, "GET", "/api/state?brief=1", nil); e == nil {
		return obj{"id": id, "status": "already connected", "panel": s.HTTP + "/#" + s.Token}, nil
	}
	if s.Stopped || s.RPC == "" {
		return nil, fail("session cannot be recovered; no target was started")
	}
	_ = os.Remove(filepath.Join(s.Dir, "error"))
	log, e := os.OpenFile(filepath.Join(s.Dir, "broker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return nil, e
	}
	defer log.Close()
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	cmd := exec.Command(exe, "serve", "--id", id, "--recover")
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	go func() { _ = cmd.Wait() }()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		current, err := readSession(id)
		if err == nil && current.PID != s.PID {
			if v, err := api(current, "GET", "/api/state?brief=1", nil); err == nil {
				return obj{"id": id, "status": v["status"], "panel": current.HTTP + "/#" + current.Token, "pid": v["state"], "message": "Broker reconnected to the existing Delve process"}, nil
			}
		}
		if data, err := os.ReadFile(filepath.Join(s.Dir, "error")); err == nil {
			return nil, fail("%s", data)
		}
	}
	return nil, fail("recovery timed out; inspect broker.log")
}
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	e := syscall.Kill(pid, 0)
	return e == nil || errors.Is(e, syscall.EPERM)
}
func cleanupSession(id string) (obj, error) {
	s, e := readSession(id)
	if e != nil {
		return nil, e
	}
	lock, e := lockSession(s.Dir)
	if e != nil {
		return nil, e
	}
	defer unlockSession(lock)
	if !s.Stopped && (processExists(s.PID) || processExists(s.DelvePID) || processExists(s.TargetPID)) {
		return nil, fail("session processes still exist; recover or stop the session before cleanup")
	}
	if e = removeZedConfig(s); e != nil {
		return nil, e
	}
	s.Stopped = true
	if e = writePrivateJSON(filepath.Join(s.Dir, "session.json"), s); e != nil {
		return nil, e
	}
	return obj{"id": id, "status": "cleaned", "message": "Removed this session's Zed profile; diagnostic logs retained"}, nil
}
func doctor(args []string) (obj, error) {
	f := flag.NewFlagSet("doctor", flag.ContinueOnError)
	binary := f.String("binary", "", "optional target")
	project := f.String("project", ".", "project")
	if e := f.Parse(args); e != nil {
		return nil, e
	}
	checks := obj{}
	for name, argv := range map[string][]string{"go": {"version"}, "dlv": {"version"}, "zed": {"--version"}, "code": {"--version"}, "codex": {"--version"}} {
		path, e := exec.LookPath(name)
		if e != nil && name == "dlv" {
			home, _ := os.UserHomeDir()
			path, e = exec.LookPath(filepath.Join(home, "go", "bin", "dlv"))
		}
		if e != nil {
			checks[name] = obj{"available": false, "error": e.Error()}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, path, argv...).CombinedOutput()
		cancel()
		checks[name] = obj{"available": err == nil, "path": path, "version": strings.TrimSpace(string(out)), "error": errorString(err)}
	}
	_, queueErr := findCodex()
	checks["automaticHandover"] = obj{"available": queueErr == nil, "error": errorString(queueErr)}
	result := obj{"checks": checks, "platforms": "macOS and Linux; Go/Delve compatibility depends on the target build"}
	if *binary != "" {
		abs, e := filepath.Abs(*binary)
		if e != nil {
			return nil, e
		}
		info, e := os.Stat(abs)
		if e != nil {
			return nil, e
		}
		result["executable"] = info.Mode()&0111 != 0
		result["debugInfo"] = hasDebugInfo(abs)
		root, _ := filepath.Abs(*project)
		result["fingerprint"] = fingerprint(abs, root)
	}
	return result, nil
}
