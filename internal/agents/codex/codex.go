package codex

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var threadUUID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

func ValidThread(s string) bool { return threadUUID.MatchString(s) }

func Find() (string, error) {
	p, e := exec.LookPath("codex")
	if e != nil {
		return "", fmt.Errorf("Codex CLI not found; use doctor to check wakeup support")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, e := exec.CommandContext(ctx, p, "queue", "--help").CombinedOutput(); e != nil || !strings.Contains(string(out), "--thread") {
		return "", fmt.Errorf("this Codex CLI does not support queue --thread")
	}
	return p, nil
}

func Message(id, session, kind string) string {
	text := "Debug Handover event " + id + " for session " + session + ". The user clicked "
	if kind == "handover" {
		text += "Hand over to Zed in the live inspector. Use the debug-handover skill to attach Zed to this existing session and verify the paused process. First read its current state; ignore this event if ownership is no longer zed or it is already attached. Do not rebuild, restart, or resume the debuggee."
	} else {
		text += "Give control to Codex in the debugger UI. Inspect the existing session with the debug-handover skill, read the fresh stack and locals, and continue the debugging discussion. First check current ownership; ignore this event if ownership is no longer codex. Do not resume execution unless the user's debugging instructions authorize it."
	}
	text += " This is a debugger UI event, not a new implementation request."
	return text
}

// Queue preserves the task's configuration and runs no shell or second agent.
func Queue(ctx context.Context, executable, thread, message string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, "queue", "--thread", thread, "--message", message).CombinedOutput()
}
