package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"agentdebugger/internal/editors/zed"
	"agentdebugger/internal/session"
)

func recoverSession(id string) (obj, error) {
	s, e := session.Read(id)
	if e != nil {
		return nil, e
	}
	if _, e = api(s, "GET", "/api/state?brief=1", nil); e == nil {
		_ = startBridge(s)
		return obj{"id": id, "status": "already connected", "panel": s.HTTP + "/#" + s.Token}, nil
	}
	if s.Stopped || s.RPC == "" {
		return nil, fmt.Errorf("session cannot be recovered; no target was started")
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
		current, err := session.Read(id)
		if err == nil && current.PID != s.PID {
			if v, err := api(current, "GET", "/api/state?brief=1", nil); err == nil {
				_ = startBridge(current)
				return obj{"id": id, "status": v["status"], "panel": current.HTTP + "/#" + current.Token, "pid": v["state"], "message": "Broker reconnected to the existing Delve process"}, nil
			}
		}
		if data, err := os.ReadFile(filepath.Join(s.Dir, "error")); err == nil {
			return nil, fmt.Errorf("%s", data)
		}
	}
	return nil, fmt.Errorf("recovery timed out; inspect broker.log")
}

func cleanupSession(id string) (obj, error) {
	s, e := session.Read(id)
	if e != nil {
		return nil, e
	}
	lock, e := session.Lock(s.Dir)
	if e != nil {
		return nil, e
	}
	defer session.Unlock(lock)
	if !s.Stopped && (session.ProcessExists(s.PID) || session.ProcessExists(s.DelvePID) || session.ProcessExists(s.TargetPID)) {
		return nil, fmt.Errorf("session processes still exist; recover or stop the session before cleanup")
	}
	if e = zed.RemoveConfig(s); e != nil {
		return nil, e
	}
	s.Stopped = true
	if e = session.Write(filepath.Join(s.Dir, "session.json"), s); e != nil {
		return nil, e
	}
	return obj{"id": id, "status": "cleaned", "message": "Removed this session's Zed profile; diagnostic logs retained"}, nil
}
