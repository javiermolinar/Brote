package broker

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"debug-handover/internal/agents/codex"
	"debug-handover/internal/editors/zed"
	"debug-handover/internal/session"
)

// Options configures a broker process; command-line parsing belongs to the CLI.
type Options struct {
	ID, Binary, Project, Delve, Thread string
	Recover                            bool
	Args                               []string
}

func (b *broker) persist() error {
	b.s.Owner = b.owner
	return session.Write(filepath.Join(b.s.Dir, "session.json"), b.s)
}

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
		return fmt.Errorf("session endpoint must use 127.0.0.1")
	}
	return nil
}

// Serve runs one session broker until it is stopped or receives a shutdown signal.
func Serve(options Options) (err error) {
	dir := filepath.Join(session.Root(), options.ID)
	lock, e := session.Lock(dir)
	if e != nil {
		return e
	}
	defer session.Unlock(lock)
	defer func() {
		if err != nil {
			_ = os.WriteFile(filepath.Join(dir, "error"), []byte(err.Error()), 0600)
		}
	}()
	s := session.Descriptor{ID: options.ID, PID: os.Getpid(), Binary: options.Binary, Project: options.Project, Dir: dir, Token: session.NewID(32), Created: time.Now().Format(time.RFC3339), Owner: "codex", Thread: options.Thread}
	var process *exec.Cmd
	committed := false
	defer func() {
		if process != nil && !committed {
			_ = process.Process.Kill()
			_ = process.Wait()
		}
	}()
	if options.Recover {
		s, e = session.Read(options.ID)
		if e != nil {
			return e
		}
		if s.Stopped {
			return fmt.Errorf("session was explicitly stopped")
		}
		if s.RPC == "" || s.TargetPID == 0 {
			return fmt.Errorf("this older session has no recovery metadata; it cannot be restarted safely")
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
			if !codex.ValidThread(s.Thread) {
				return fmt.Errorf("invalid task UUID")
			}
			s.Codex, e = codex.Find()
			if e != nil {
				return e
			}
		}
		log, e := os.OpenFile(filepath.Join(dir, "delve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return e
		}
		defer log.Close()
		argv := append([]string{"exec", s.Binary, "--headless", "--listen=127.0.0.1:0", "--api-version=2", "--accept-multiclient", "--"}, options.Args...)
		process = exec.Command(options.Delve, argv...)
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
			return fmt.Errorf("Delve did not start; see %s", filepath.Join(dir, "delve.log"))
		}
		s.Fingerprint = session.CaptureFingerprint(s.Binary, s.Project)
	}
	b := &broker{s: s, rpcAddr: s.RPC, owner: s.Owner, generation: int(time.Now().UnixMilli()), done: make(chan struct{})}
	if b.owner == "" {
		b.owner = "codex"
	}
	state, e := b.state()
	if e != nil {
		return fmt.Errorf("Delve is unavailable; debuggee was not relaunched: %w", e)
	}
	if options.Recover && num(state["Pid"]) != s.TargetPID {
		return fmt.Errorf("Delve process identity differs; refusing recovery")
	}
	b.s.TargetPID = num(state["Pid"])
	if options.Recover && b.owner == "vscode" {
		b.s.HandoverID = session.NewID(8)
	}
	if !options.Recover {
		b.captureSources()
	}
	if b.s.DAP != "" {
		if e = validateLoopback(b.s.DAP); e != nil {
			return e
		}
	}
	dapListener, e := listenSession(b.s.DAP)
	if e != nil {
		return e
	}
	defer dapListener.Close()
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
	b.s.DAP = dapListener.Addr().String()
	b.s.HTTP = "http://" + httpLn.Addr().String()
	// Only refresh a profile already created for this session. Starting a session
	// does not need to alter the user's Zed configuration.
	if options.Recover && zed.HasProfile(b.s) {
		if _, e = zed.WriteConfig(b.s); e != nil {
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
	go b.acceptDAP(dapListener)
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
