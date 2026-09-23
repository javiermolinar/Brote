package broker

import (
	"agentdebugger/internal/backend"
	"agentdebugger/internal/delve"
	"agentdebugger/internal/session"
	"agentdebugger/internal/telemetry"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// restartLocked replaces a launched target while preserving the service and
// credential. Callers hold b.mu, so a partially started run is never observable.
func (b *broker) restartLocked(keepEditor ...bool) (out obj, err error) {
	if b.s.ServiceVersion == 0 || b.backend == nil {
		return nil, fmt.Errorf("restart requires a shared-service DAP session")
	}
	if b.s.Attached {
		return nil, fmt.Errorf("cannot restart an externally attached process; detach and attach explicitly")
	}
	settings, err := session.ReadLaunchSettings(b.s.Dir)
	if err != nil {
		return nil, err
	}
	if settings == nil || settings.Delve == "" {
		return nil, fmt.Errorf("saved launch settings unavailable; start a new session")
	}
	if st, e := os.Stat(b.s.Binary); e != nil || st.IsDir() || st.Mode()&0111 == 0 {
		return nil, fmt.Errorf("saved executable unavailable")
	}
	if _, e := exec.LookPath(settings.Delve); e != nil {
		return nil, e
	}
	definitions, err := b.backend.Call("ListBreakpoints", obj{})
	if err != nil {
		return nil, err
	}
	b.trace.Close()
	old := b.backend
	if _, err = old.Call("Detach", obj{"Kill": true}); err != nil {
		if _, exited := delve.ExitState(err); !exited && !truth(old.State()["exited"]) {
			return nil, err
		}
	}
	if b.peer != nil && (len(keepEditor) == 0 || !keepEditor[0]) {
		b.peer.close()
		b.peer = nil
	}
	old.Close()
	b.backend = nil
	if b.process != nil {
		_ = b.process.Process.Kill()
		_ = b.process.Wait()
		b.process = nil
	}
	b.cancelTask("run restarted")
	b.moving = false
	b.interrupting = false
	b.handleEpoch++
	b.generation++
	b.s.RunID = session.NewID(16)
	b.s.TraceIDs = telemetry.IDs{}
	b.s.Definitions = b.s.Definitions.ForRun(b.s.RunID)
	b.s.RPC = ""
	b.s.TargetPID = 0
	b.s.DelvePID = 0
	b.s.Stopped = false
	b.s.BreakpointOwners = nil
	b.s.FunctionBreakpoints = nil
	b.lastError = ""
	b.seenCommands = nil
	b.s.ExecutionCommands = nil
	b.s.CaptureCounts = nil
	b.currentStop = stopAttribution{}
	// A failed replacement leaves an ended descriptor, never a live-looking
	// record pointing at the old PID or an unregistered new child.
	defer func() {
		if err == nil {
			return
		}
		if b.backend != nil {
			_, _ = b.backend.Call("Detach", obj{"Kill": true})
			b.backend.Close()
			b.backend = nil
		}
		if b.process != nil {
			_ = b.process.Process.Kill()
			_ = b.process.Wait()
			b.process = nil
		}
		b.s.Stopped = true
		b.lastError = err.Error()
		_ = b.persist()
		go func() { time.Sleep(150 * time.Millisecond); b.once.Do(func() { close(b.done) }) }()
	}()
	b.process, err = startTarget(&b.s, Options{Binary: b.s.Binary, Project: b.s.Project, Delve: settings.Delve, Args: settings.Args}, settings)
	if err != nil {
		return nil, err
	}
	b.rpcAddr = b.s.RPC
	b.backend, err = backend.OpenWithMapping(b.s.RPC, nil, nil, settings.SubstitutePath)
	if err != nil {
		return nil, err
	}
	b.s.TargetPID = num(b.backend.State()["Pid"])
	b.s.Fingerprint = session.CaptureFingerprint(b.s.Binary, b.s.Project)
	if err = b.backend.RestoreBreakpoints(asList(definitions["Breakpoints"])); err != nil {
		return nil, err
	}
	if err = b.reconcileDefinitions(); err != nil {
		return nil, err
	}
	if err = b.persist(); err != nil {
		return nil, err
	}
	b.openTrace(settings)
	if err = b.persist(); err != nil {
		return nil, err
	}
	b.record("run.restarted", "core", obj{"run": b.s.RunID})
	b.watchBackend(b.backend)
	return obj{"id": b.s.ID, "run": b.s.RunID, "generation": b.generation, "status": "paused"}, nil
}
