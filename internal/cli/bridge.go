package cli

import (
	"agentdebugger/internal/agents/codex"
	"agentdebugger/internal/session"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

type bridgeConfig struct {
	Thread     string          `json:"thread"`
	Executable string          `json:"executable"`
	Binding    session.Binding `json:"binding"`
	Cursor     uint64          `json:"cursor"`
}

func validCodexThread(s string) bool { return codex.ValidThread(s) }
func configureBridge(s session.Descriptor, thread string) error {
	executable, err := codex.Find()
	if err != nil {
		return err
	}
	if s.Binding == nil {
		return fmt.Errorf("session has no binding")
	}
	cfg := bridgeConfig{Thread: thread, Executable: executable, Binding: *s.Binding, Cursor: s.Cursor}
	if err = session.Write(filepath.Join(s.Dir, "bridge.json"), cfg); err != nil {
		return err
	}
	if err = session.Write(filepath.Join(s.Dir, fmt.Sprintf("bridge-%d.json", s.Binding.Revision)), cfg); err != nil {
		return err
	}
	return startBridge(s)
}
func startBridge(s session.Descriptor) error {
	data, err := os.ReadFile(filepath.Join(s.Dir, "bridge.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var configured bridgeConfig
	if err = json.Unmarshal(data, &configured); err != nil {
		return err
	}
	if s.Binding == nil || *s.Binding != configured.Binding {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(s.Dir, "bridge.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	if s.Binding == nil {
		return fmt.Errorf("missing binding")
	}
	cmd := exec.Command(exe, "bridge", s.ID, strconv.FormatUint(s.Binding.Revision, 10))
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
func bridge(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("bridge requires session ID and revision")
	}
	s, err := session.Read(args[0])
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(s.Dir, "bridge-lock"), 0700); err != nil {
		return err
	}
	revision, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return err
	}
	lockDir := filepath.Join(s.Dir, fmt.Sprintf("bridge-lock/%d", revision))
	if err = os.MkdirAll(lockDir, 0700); err != nil {
		return err
	}
	lock, err := session.Lock(lockDir)
	if err != nil {
		return err
	}
	defer session.Unlock(lock)
	cfgPath := filepath.Join(s.Dir, fmt.Sprintf("bridge-%d.json", revision))
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	var cfg bridgeConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	// Shared sessions use the common Go consumer; old cursor files are only
	// compatibility input. Pending subjects reconcile independently of the cursor.
	if s.ServiceVersion > 0 {
		state, err := api(s, "GET", "/api/state?brief=1", nil)
		if err != nil {
			return err
		}
		caps, _ := state["capabilities"].(map[string]any)
		if caps["coordination"] == float64(1) {
			return managedCodex(s, cfg)
		}
		return fmt.Errorf("shared service lacks managed coordination; update and recover the session")
	}
	return legacyBridge(s, cfg, cfgPath)
}
