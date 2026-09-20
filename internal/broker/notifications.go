package broker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"debug-handover/internal/agents/codex"
	"debug-handover/internal/session"
)

func (b *broker) queueNotification(kind string) error {
	if b.s.Thread == "" {
		return fmt.Errorf("no Codex task is bound; use bind ID --thread UUID")
	}
	// An unresolved delivery must be acknowledged by the user, never auto-retried:
	// an interrupted CLI can have submitted successfully before losing its reply.
	if n := b.s.Notification; n != nil && n.Kind == kind && (n.Status == "sending" || n.Status == "pending") {
		return fmt.Errorf("a Codex notification is already being sent")
	}
	// A newer ownership transfer may supersede an in-flight event. The task checks
	// event IDs and current ownership, so the old event cannot attach the wrong side.
	n := &session.Notification{ID: session.NewID(8), Kind: kind, Status: "pending", Created: time.Now().Format(time.RFC3339)}
	b.s.Notification = n
	if e := b.persist(); e != nil {
		return e
	}
	go b.deliverNotification(n.ID)
	return nil
}

func (b *broker) deliverNotification(id string) {
	b.mu.Lock()
	n := b.s.Notification
	if n == nil || n.ID != id {
		b.mu.Unlock()
		return
	}
	n.Status = "sending"
	if e := b.persist(); e != nil {
		n.Status = "failed"
		n.Error = e.Error()
		b.mu.Unlock()
		return
	}
	thread, executable, sessionID, kind := b.s.Thread, b.s.Codex, b.s.ID, n.Kind
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, e := codex.Queue(ctx, executable, thread, codex.Message(id, sessionID, kind))
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.s.Notification == nil || b.s.Notification.ID != id {
		return
	}
	if e == nil {
		n.Status = "queued"
		n.Error = ""
	} else {
		n.Status = "failed"
		n.Error = strings.TrimSpace(string(output))
		if n.Error == "" {
			n.Error = e.Error()
		}
		if len(n.Error) > 1024 {
			n.Error = n.Error[:1024]
		}
		if ctx.Err() != nil {
			n.Status = "unknown"
			n.Error = "delivery timed out; check the task before retrying to avoid duplicate messages"
		}
	}
	if err := b.persist(); err != nil {
		b.lastError = "could not save notification status: " + err.Error()
	}
}
