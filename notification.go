package main

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type Notification struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Created string `json:"created"`
}

var threadUUID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func validThread(s string) bool { return threadUUID.MatchString(s) }
func findCodex() (string, error) {
	p, e := exec.LookPath("codex")
	if e != nil {
		return "", fail("Codex CLI not found; use doctor to check wakeup support")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, e := exec.CommandContext(ctx, p, "queue", "--help").CombinedOutput(); e != nil || !strings.Contains(string(out), "--thread") {
		return "", fail("this Codex CLI does not support queue --thread")
	}
	return p, nil
}
func (b *Broker) queueNotification(kind string) error {
	if b.s.Thread == "" {
		return fail("no Codex task is bound; use bind ID --thread UUID")
	}
	// An unresolved delivery must be acknowledged by the user, never auto-retried:
	// an interrupted CLI can have submitted successfully before losing its reply.
	if n := b.s.Notification; n != nil && n.Kind == kind && (n.Status == "sending" || n.Status == "pending") {
		return fail("a Codex notification is already being sent")
	}
	// A newer ownership transfer may supersede an in-flight event. The task checks
	// event IDs and current ownership, so the old event cannot attach the wrong side.
	n := &Notification{ID: randomID(8), Kind: kind, Status: "pending", Created: time.Now().Format(time.RFC3339)}
	b.s.Notification = n
	if e := b.persist(); e != nil {
		return e
	}
	go b.deliverNotification(n.ID)
	return nil
}
func (b *Broker) deliverNotification(id string) {
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
	thread, codex, session, kind := b.s.Thread, b.s.Codex, b.s.ID, n.Kind
	b.mu.Unlock()
	text := "Debug Handover event " + id + " for session " + session + ". The user clicked "
	if kind == "handover" {
		text += "Hand over to Zed in the live inspector. Use the debug-handover skill to attach Zed to this existing session and verify the paused process. First read its current state; ignore this event if ownership is no longer zed or it is already attached. Do not rebuild, restart, or resume the debuggee."
	} else {
		text += "Give control to Codex in the debugger UI. Inspect the existing session with the debug-handover skill, read the fresh stack and locals, and continue the debugging discussion. First check current ownership; ignore this event if ownership is no longer codex. Do not resume execution unless the user's debugging instructions authorize it."
	}
	text += " This is a debugger UI event, not a new implementation request."
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, codex, "queue", "--thread", thread, "--message", text)
	// Preserve the user's Codex configuration and task settings; do not add model,
	// approval, sandbox, or resume flags. No shell or second Codex agent runs work.
	output, e := cmd.CombinedOutput()
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
