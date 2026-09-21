package cli

import (
	"agentdebugger/internal/agents/codex"
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
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
	for {
		s, err = session.Read(s.ID)
		if err != nil {
			return err
		}
		if s.Stopped {
			return nil
		}
		if s.Binding == nil || *s.Binding != cfg.Binding {
			return nil
		}
		if err = deliverQuestions(s, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		if err = deliverTask(s, cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		err = stream(context.Background(), s, cfg.Cursor, cfg.Binding.ID, func(event session.Event) error {
			if event.Binding == nil || *event.Binding != cfg.Binding {
				return fmt.Errorf("binding changed")
			}
			if event.Kind == "task.authorized" {
				if err := deliverTask(s, cfg); err != nil {
					return err
				}
			}
			if event.Kind == "question.created" {
				if err := deliverQuestions(s, cfg); err != nil {
					return err
				}
			}
			if event.Kind == "control_returned" {
				if err := deliverCodex(s, cfg, event); err != nil {
					return err
				}
			}
			cfg.Cursor = event.ID
			return session.Write(cfgPath, cfg)
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		// Reconcile bounded journal expiration from durable current state, not stale events.
		s, err = session.Read(s.ID)
		if err != nil {
			return err
		}
		if s.Binding == nil || *s.Binding != cfg.Binding || s.Stopped {
			return nil
		}
		if len(s.Events) > 0 && cfg.Cursor < s.Events[0].ID-1 {
			for _, event := range s.Events {
				if event.Kind == "control_returned" && s.Notification != nil && s.Notification.ID == strconv.FormatUint(event.ID, 10) {
					_ = deliverCodex(s, cfg, event)
				}
			}
			cfg.Cursor = s.Cursor
			if err = session.Write(cfgPath, cfg); err != nil {
				return err
			}
		}
		time.Sleep(time.Second)
	}
}
func deliveryStatus(s session.Descriptor, cfg bridgeConfig, id, status, message string) error {
	state, err := api(s, "GET", "/api/state?brief=1", nil)
	if err != nil {
		return err
	}
	_, err = api(s, "POST", "/api/action", obj{"action": "event-status", "generation": state["generation"], "binding": cfg.Binding.ID, "revision": cfg.Binding.Revision, "event": id, "status": status, "error": message})
	return err
}
func deliverCodex(s session.Descriptor, cfg bridgeConfig, event session.Event) error {
	state, err := api(s, "GET", "/api/state?brief=1", nil)
	if err != nil {
		return err
	}
	id := strconv.FormatUint(event.ID, 10)
	binding, _ := state["binding"].(map[string]any)
	notification, _ := state["notification"].(map[string]any)
	if str(state["owner"]) != "agent" || str(binding["id"]) != cfg.Binding.ID || binding["revision"] != float64(cfg.Binding.Revision) || str(notification["id"]) != id {
		return nil
	}
	if str(notification["status"]) == "sending" {
		return deliveryStatus(s, cfg, id, "unknown", "listener interrupted during delivery; check the conversation before retrying")
	}
	if str(notification["status"]) != "pending" {
		return nil
	}
	if err = deliveryStatus(s, cfg, id, "sending", ""); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	message := fmt.Sprintf("Brote event %s for session %s (binding %s revision %d). The human returned control. Read fresh state; ignore if owner, binding, or event changed. Inspect the fresh stack and locals, then acknowledge with event-status --event %s --revision %d --status acknowledged. Never resume without the user's debugging authorization. Handover note: %s", id, s.ID, cfg.Binding.ID, cfg.Binding.Revision, id, cfg.Binding.Revision, event.Note)
	output, err := codex.Queue(ctx, cfg.Executable, cfg.Thread, message)
	status, detail := "queued", ""
	if err != nil {
		status = "failed"
		detail = string(output)
		if detail == "" {
			detail = err.Error()
		}
		if ctx.Err() != nil {
			status = "unknown"
			detail = "delivery timed out; check conversation before retrying"
		}
	}
	return deliveryStatus(s, cfg, id, status, detail)
}
