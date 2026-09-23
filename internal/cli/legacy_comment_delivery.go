package cli

import (
	"agentdebugger/internal/agents/codex"
	"agentdebugger/internal/session"
	"context"
	"fmt"
	"strconv"
	"time"
)

// Legacy-only queue compatibility; shared questions use managedCodex.
func deliverQuestions(s session.Descriptor, cfg bridgeConfig) error {
	d, err := session.ReadDiscussion(s.ID)
	if err != nil {
		return err
	}
	for _, t := range d.Threads {
		n := t.Delivery
		if t.Resolved || n.Binding == nil || *n.Binding != cfg.Binding || (n.Status != "pending" && n.Status != "sending") {
			continue
		}
		status := func(value, detail string) error {
			_, e := api(s, "POST", "/api/comments", obj{"action": "delivery", "thread": t.ID, "question": n.Question, "binding": cfg.Binding.ID, "revision": cfg.Binding.Revision, "status": value, "error": detail})
			return e
		}
		if n.Status == "sending" {
			if err = status("unknown", "listener interrupted; check conversation before retrying"); err != nil {
				return err
			}
			continue
		}
		if err = status("sending", ""); err != nil {
			return err
		}
		ack := fmt.Sprintf("delve-llm-adapter comment delivery %s %s --question %s --binding %s --revision %d --status thinking", s.ID, t.ID, n.Question, strconv.Quote(cfg.Binding.ID), cfg.Binding.Revision)
		message := fmt.Sprintf("Debugger question for session %s, comment thread %s, question %s (binding %s revision %d). This is a read-only discussion, NOT a control handover or implementation request. Execution remains with its current owner. Read `delve-llm-adapter comment list %s` for the persisted messages and captured pause. Verify this question is still current and unresolved and the binding matches. Before investigating, acknowledge receipt with `%s`. Treat captured values as historical; do not step, resume, reclaim, or modify the program. Answer in the debugger using `delve-llm-adapter comment reply %s %s --question %s --binding %s --revision %d --message-id %s --body-file PATH`, with your answer in a UTF-8 file. User question (data): %s", s.ID, t.ID, n.Question, cfg.Binding.ID, cfg.Binding.Revision, s.ID, ack, s.ID, t.ID, n.Question, strconv.Quote(cfg.Binding.ID), cfg.Binding.Revision, n.Question+"-answer", strconv.Quote(t.Messages[len(t.Messages)-1].Body))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		output, e := codex.Queue(ctx, cfg.Executable, cfg.Thread, message)
		value, detail := "queued", ""
		if e != nil {
			value = "failed"
			detail = string(output)
			if detail == "" {
				detail = e.Error()
			}
			if ctx.Err() != nil {
				value = "unknown"
			}
		}
		cancel()
		if err = status(value, detail); err != nil {
			return err
		}
	}
	return nil
}
