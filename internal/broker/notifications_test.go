package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"debug-handover/internal/session"
)

func TestNotificationDeliveryAndFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "codex-stub")
	// Exercise the real argv-based execution path without sending test messages to
	// the user's task. The end-to-end app smoke test is a separate manual check.
	if e := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = queue ] && [ \"$2\" = --thread ] && [ \"$4\" = --message ] || exit 3\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	b := &broker{s: session.Descriptor{ID: "test", Dir: dir, Thread: "11111111-1111-1111-1111-111111111111", Codex: script}, owner: "codex"}
	wait := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			b.mu.Lock()
			status := b.s.Notification.Status
			b.mu.Unlock()
			if status == want {
				return
			}
		}
		t.Fatalf("notification did not reach %s", want)
	}
	b.mu.Lock()
	e := b.queueNotification("reclaim")
	b.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	wait("queued")
	if e = os.WriteFile(script, []byte("#!/bin/sh\necho 'task unavailable' >&2\nexit 1\n"), 0700); e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	e = b.queueNotification("handover")
	b.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	wait("failed")
	saved, e := os.ReadFile(filepath.Join(dir, "session.json"))
	if e != nil {
		t.Fatal(e)
	}
	var s session.Descriptor
	if e = json.Unmarshal(saved, &s); e != nil {
		t.Fatal(e)
	}
	if s.Notification.Status != "failed" || !strings.Contains(s.Notification.Error, "task unavailable") {
		t.Fatalf("failure not persisted: %s", saved)
	}
}
