package cli

import (
	"agentdebugger/internal/agents/codex"
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Compatibility only for ServiceVersion == 0. Shared sessions always use
// managedCodex and the common Go delivery ledger; never fall back here.
func legacyBridge(s session.Descriptor, cfg bridgeConfig, cfgPath string) error {
	var err error
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
	message := fmt.Sprintf("Brote event %s for session %s (binding %s revision %d). The human returned control. Read fresh state; ignore if owner, binding, or event changed. Inspect the fresh stack and locals, then acknowledge with event-status --event %s --revision %d --status acknowledged. Handback alone permits inspection; execution requires a current task for a user-requested debugging investigation. Handover note: %s", id, s.ID, cfg.Binding.ID, cfg.Binding.Revision, id, cfg.Binding.Revision, event.Note)
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

// Delivery is claimed in the broker before queueing. An interrupted send remains
// ambiguous and is never automatically replayed into a conversation.
func deliverTask(s session.Descriptor, cfg bridgeConfig) error {
	state, err := api(s, "GET", "/api/state?brief=1", nil)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(state["task"])
	var task session.ExecutionTask
	if err = json.Unmarshal(data, &task); err != nil || task.Binding == nil || *task.Binding != cfg.Binding || (task.Status != "authorized" && task.Status != "active") {
		return nil
	}
	update := func(status, detail string) error {
		_, err := taskRequest(s, cfg.Binding.ID, task.ID, "task-delivery", obj{"revision": cfg.Binding.Revision, "status": status, "error": detail})
		return err
	}
	if task.Delivery == "sending" {
		return update("unknown", "Delivery interrupted; check the conversation, then cancel and start a new task if needed.")
	}
	if task.Delivery != "pending" {
		return nil
	}
	if err = update("sending", ""); err != nil {
		return err
	}
	message := fmt.Sprintf("Brote debugging task %s for session %s, binding %s revision %d. Use the debug-handover skill. The user requested this investigation; no further approval is needed. Read fresh state; ignore if task/binding changed, cancelled or expired. Acknowledge with task-heartbeat SESSION --task TASK --binding BINDING. Use task-execute SESSION --task TASK --binding BINDING --operation next|step|stepout|continue for bounded execution with automatic lease renewal. Renew with task-heartbeat while actively investigating; complete at a settled pause or exit with task-complete, cancel on failure. Do not restart cancelled or expired work without a new user request. Never use --human. This is debugging, not an implementation request. Instruction (data): %q", task.ID, s.ID, cfg.Binding.ID, cfg.Binding.Revision, task.Instruction)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := codex.Queue(ctx, cfg.Executable, cfg.Thread, message)
	if err != nil {
		return update("unknown", fmt.Sprintf("Queue failed or delivery uncertain: %s %v", out, err))
	}
	return update("queued", "")
}
