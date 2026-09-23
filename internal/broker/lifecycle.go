package broker

import (
	"context"
	"errors"
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

	"agentdebugger/internal/backend"
	"agentdebugger/internal/delve"
	"agentdebugger/internal/editors/zed"
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"agentdebugger/internal/telemetry"
	"agentdebugger/internal/tracing"
)

// Options configures a broker process; command-line parsing belongs to the CLI.
type Options struct {
	Backend                            string
	ID, Binary, Project, Delve, Thread string
	BindingID, AgentName               string
	AttachPID                          int
	Service                            bool
	EditorStartup                      bool
	Recover                            bool
	Args                               []string
}

func (b *broker) persist() error {
	b.s.Owner = b.owner
	if b.backend != nil {
		b.s.BreakpointOwners = b.backend.BreakpointOwners()
		b.s.FunctionBreakpoints = b.backend.FunctionBreakpoints()
	}
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
	if options.EditorStartup && (!options.Service || options.AttachPID > 0 || options.Recover) {
		return fmt.Errorf("editor startup lease requires a new shared-service launch")
	}
	if options.AttachPID < 0 || (options.AttachPID > 0 && (!options.Service || len(options.Args) > 0)) {
		return fmt.Errorf("process attach requires service mode and no program arguments")
	}
	if options.Service && options.Backend != "dap" {
		return fmt.Errorf("shared service sessions require the DAP backend")
	}
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
	s := session.Descriptor{Backend: options.Backend, ID: options.ID, PID: os.Getpid(), Binary: options.Binary, Project: options.Project, Dir: dir, Version: 2, Binding: &session.Binding{ID: options.BindingID, Revision: 1, Name: options.AgentName}, Created: time.Now().Format(time.RFC3339), Owner: "agent"}
	defer func() {
		if err != nil && !options.Recover {
			if failure := session.RecordFailedLaunch(s, options.Args, err.Error()); failure != nil {
				err = fmt.Errorf("%w; failed launch record: %v", err, failure)
			}
		}
	}()
	s.Attached = options.AttachPID > 0
	var process *exec.Cmd
	var settings *session.LaunchSettings
	committed := false
	defer func() {
		if process != nil && process.Process != nil && !committed {
			if s.RPC != "" {
				_, _ = delve.Call(s.RPC, "Detach", obj{"Kill": !s.Attached}, 3*time.Second)
			}
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
			n.Error = "broker stopped during delivery; check the bound conversation before retrying"
		}
	} else {
		settings, e = session.ReadLaunchSettings(dir)
		if e != nil {
			return fmt.Errorf("read launch settings: %w", e)
		}
		settingsForRun := settings
		if settingsForRun == nil {
			settingsForRun = &session.LaunchSettings{}
		}
		settingsForRun.Args = append([]string{}, options.Args...)
		settingsForRun.Delve = options.Delve
		settings = settingsForRun
		settings.Service = options.Service
		if options.Service && settings.OTLP == nil && settings.OTLPError == "" {
			config, exportErr := telemetry.FromEnvironment(settings.Environment(os.Environ()))
			settings.OTLP = config
			if exportErr != nil {
				settings.OTLPError = exportErr.Error()
			}
		}
		if e = session.Write(filepath.Join(s.Dir, "launch.json"), settings); e != nil {
			return e
		}
		process, e = startTarget(&s, options, settings)
		if e != nil {
			return e
		}
		s.Fingerprint = session.CaptureFingerprint(s.Binary, s.Project)
	}
	// A persisted active fact is not proof that the host survived recovery.
	for id, c := range s.Consumers {
		c.Host = nil
		c.Challenge = ""
		c.ChallengeExpires = ""
		s.Consumers[id] = c
	}
	if s.Binding == nil {
		s.Binding = &session.Binding{ID: session.NewID(16), Revision: 1, Name: "Agent"}
	}
	if s.Binding.ID == "" {
		s.Binding.ID = session.NewID(16)
	}
	if s.Binding.Name == "" {
		s.Binding.Name = "Agent"
	}
	if s.Task != nil && (s.Task.Status == "active" || s.Task.Status == "authorized") {
		s.Task.Status = "cancelled"
		s.Task.Reason = "broker recovered; resume debugging only at the user's request"
	}
	s.Version = 2
	if !options.Recover && options.Service {
		s.ServiceVersion = 1
		s.RunID = session.NewID(16)
		s.Token = session.NewID(32)
	}
	s.Thread, s.Codex = "", ""
	if s.Owner == "codex" {
		s.Owner = "agent"
	}
	b := &broker{process: process, changed: make(chan struct{}), s: s, rpcAddr: s.RPC, owner: s.Owner, generation: int(time.Now().UnixMilli()), done: make(chan struct{})}
	b.seenCommands = map[string]bool{}
	for _, id := range s.ExecutionCommands {
		b.seenCommands[id] = true
	}
	if b.owner == "" {
		b.owner = "agent"
	}
	if s.Backend == "dap" {
		if options.Recover {
			settings, e = session.ReadLaunchSettings(s.Dir)
			if e != nil {
				return e
			}
		}
		var mapping []protocol.PathMapping
		if settings != nil {
			mapping = settings.SubstitutePath
		}
		b.backend, e = backend.OpenWithMapping(s.RPC, s.BreakpointOwners, s.FunctionBreakpoints, mapping)
		if errors.Is(e, backend.ErrRunning) && options.Recover && s.ServiceVersion == 0 {
			if !session.ProcessExists(s.DelvePID) || !session.ProcessExists(s.TargetPID) {
				return fmt.Errorf("recovery process identity unavailable")
			}
			b.s.Backend = "rpc"
		} else if e != nil {
			return e
		}
		if b.backend != nil {
			defer func() {
				if b.backend != nil {
					b.backend.Close()
				}
			}()
		}

	}

	state, e := b.state()
	if e != nil {
		return fmt.Errorf("Delve is unavailable; debuggee was not relaunched: %w", e)
	}
	if options.Recover && num(state["Pid"]) != s.TargetPID {
		return fmt.Errorf("Delve process identity differs; refusing recovery")
	}
	if b.s.ServiceVersion > 0 {
		if e = b.reconcileDefinitions(); e != nil {
			return fmt.Errorf("restore definitions: %w", e)
		}
	}
	b.s.TargetPID = num(state["Pid"])
	if !options.Recover && options.AttachPID > 0 && b.s.TargetPID != options.AttachPID {
		return fmt.Errorf("attached process identity differs")
	}
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
	b.history, e = session.OpenHistory(b.s, options.Args)
	if e != nil {
		return fmt.Errorf("open session history: %w", e)
	}
	defer b.history.Close()
	if settings != nil {
		if err := session.Write(filepath.Join(b.history.Dir, "launch.json"), settings); err != nil {
			return fmt.Errorf("save launch settings: %w", err)
		}
	}
	b.openTrace(settings)
	if b.s.ServiceVersion == 0 {
		b.traces = tracing.NewRecorder(b.s.ID, filepath.Base(b.s.Binary), "delve")
		defer b.traces.Close()
	}
	// Publish only after the settings needed by Run again are safely archived.
	if e = b.persist(); e != nil {
		return e
	}
	b.record("broker.connected", "core", obj{"recovered": options.Recover})
	if discussion, err := session.ReadDiscussion(b.s.ID); err != nil {
		return err
	} else if len(discussion.Threads) > 0 {
		changed := false
		for i := range discussion.Threads {
			d := &discussion.Threads[i].Delivery
			if d.Status == "sending" {
				d.Status = "unknown"
				d.Error = "broker restarted during send; inspect conversation before retrying"
				changed = true
			}
		}
		if changed {
			if err := session.CommitDiscussion(&discussion); err != nil {
				return err
			}
		}
		b.historyDiscussion(discussion, "restored")
	}
	if stateStatus(state, false) == "paused" {
		b.historyStop("entry")
	}
	b.watchBackend(b.backend)

	committed = true
	go b.maintainTasks()
	defer b.once.Do(func() { close(b.done) })
	go func() { _ = server.Serve(httpLn) }()
	go b.acceptDAP(dapListener)
	if options.EditorStartup {
		b.awaitEditorConfiguration(30 * time.Second)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(sig)
	select {
	case <-b.done:
	case <-sig:
	}
	// Let in-flight actions finish writing their responses before closing the
	// broker. A stop action may still be persisting history after signaling done.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	_ = server.Shutdown(shutdownCtx)
	cancelShutdown()
	_ = server.Close()
	b.mu.Lock()
	b.closing = true
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
	b.record("broker.disconnected", "core", obj{"target_ended": b.s.Stopped})
	_ = b.persist()
	stopped := b.s.Stopped
	b.mu.Unlock()
	b.captureWorkers.Wait()
	b.trace.Close()
	if b.process != nil && stopped {
		_ = b.process.Process.Kill()
		_ = b.process.Wait()
	}
	return nil
}
