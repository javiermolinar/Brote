package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyExpressions(t *testing.T) {
	for _, expression := range []string{"x", "x.Field[3]", "xs[128:256]", "len(xs)", "cap(xs)", "a+b*2", "*ptr", "len([]int{1,2})"} {
		if e := validateExpression(expression); e != nil {
			t.Fatalf("%s: %v", expression, e)
		}
	}
	for _, expression := range []string{"", "x=2", "<-ch", "mutate()", "pkg.Mutate()", "append(xs, 1)", "func() int {return 2}()", "call f()"} {
		if e := validateExpression(expression); e == nil {
			t.Fatalf("accepted %s", expression)
		}
	}
}
func TestProfileUpdateAndCleanupPreserveOtherEntries(t *testing.T) {
	for _, data := range []string{
		`[/* keep */ {"label":"mine","nested":{"x":1,},}, {"label":"handover","adapter":"Delve","tcp_connection":{"port":1}}, // keep too
]`,
		`[{"label":"handover"}, /* between */ {"label":"mine"}]`,
		`[{"label":"handover"}]`,
		`[{"label":"mine"}, {"label":"handover"},]`,
	} {
		updated, e := appendZedProfile([]byte(data), obj{"label": "handover", "adapter": "Delve", "tcp_connection": obj{"port": 2}})
		if e != nil {
			t.Fatal(e)
		}
		cleaned, e := removeZedProfile(updated, "handover")
		if e != nil {
			t.Fatal(e)
		}
		clean, _ := stripComments(cleaned)
		var entries []obj
		if e = json.Unmarshal(stripTrailingCommas(clean), &entries); e != nil {
			t.Fatalf("invalid cleanup %s: %v", cleaned, e)
		}
		for _, entry := range entries {
			if str(entry["label"]) != "mine" {
				t.Fatalf("wrong entry: %v", entry)
			}
		}
		if strings.Contains(data, "/* keep */") && !strings.Contains(string(cleaned), "/* keep */") {
			t.Fatal("lost unrelated comment")
		}
		again, e := removeZedProfile(cleaned, "handover")
		if e != nil || string(again) != string(cleaned) {
			t.Fatal("cleanup not idempotent")
		}
	}
}
func TestNotificationDeliveryAndFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "codex-stub")
	// Exercise the real argv-based execution path without sending test messages to
	// the user's task. The end-to-end app smoke test is a separate manual check.
	if e := os.WriteFile(script, []byte("#!/bin/sh\n[ \"$1\" = queue ] && [ \"$2\" = --thread ] && [ \"$4\" = --message ] || exit 3\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	b := &Broker{s: Session{ID: "test", Dir: dir, Thread: "11111111-1111-1111-1111-111111111111", Codex: script}, owner: "codex"}
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
	var s Session
	if e = json.Unmarshal(saved, &s); e != nil {
		t.Fatal(e)
	}
	if s.Notification.Status != "failed" || !strings.Contains(s.Notification.Error, "task unavailable") {
		t.Fatalf("failure not persisted: %s", saved)
	}
}
func TestSourceFingerprintDetectsChanges(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	binary := filepath.Join(dir, "demo")
	os.WriteFile(file, []byte("package main"), 0600)
	os.WriteFile(binary, []byte("binary"), 0700)
	p := fingerprint(binary, dir)
	h, _ := fileHash(file)
	p.Sources[file] = h
	b := &Broker{s: Session{Binary: binary, Fingerprint: p}}
	if truth(b.sourceIdentity(file, []byte("package main"))["changedSinceStart"]) {
		t.Fatal("unchanged source marked changed")
	}
	if !truth(b.sourceIdentity(file, []byte("package changed"))["changedSinceStart"]) {
		t.Fatal("source change missed")
	}
	os.WriteFile(binary, []byte("changed binary"), 0700)
	if !truth(b.sourceIdentity(file, nil)["binaryChanged"]) {
		t.Fatal("binary change missed")
	}
}
