package broker

import (
	"agentdebugger/internal/backend"
	"agentdebugger/internal/session"
	"strings"
	"testing"
)

func TestOldRunCannotPauseReplacement(t *testing.T) {
	b := &broker{s: session.Descriptor{ID: "s", RunID: "new", ServiceVersion: 1}, generation: 10}
	_, err := b.action(obj{"action": "pause", "actor": "human", "run": "old", "generation": 9})
	if err == nil || !strings.Contains(err.Error(), "run identity") {
		t.Fatalf("old pause accepted: %v", err)
	}
	if b.generation != 10 {
		t.Fatal("rejected request changed state")
	}
}
func TestAttachedRestartDoesNotTouchBackend(t *testing.T) {
	b := &broker{s: session.Descriptor{ID: "s", RunID: "r", ServiceVersion: 1, Attached: true}, backend: &backend.Delve{}}
	_, err := b.restartLocked()
	if err == nil || !strings.Contains(err.Error(), "externally attached") {
		t.Fatal(err)
	}
	if b.s.RunID != "r" || b.s.Stopped {
		t.Fatal("rejected restart mutated target identity")
	}
}

func TestLaunchedDetachRejectedBeforeBackendOrScopeMutation(t *testing.T) {
	b := &broker{s: session.Descriptor{ID: "s", RunID: "r", ServiceVersion: 1}, generation: 1}
	_, err := b.action(obj{"action": "detach", "actor": "human", "run": "r", "generation": 1})
	if err == nil || !strings.Contains(err.Error(), "externally attached") {
		t.Fatal(err)
	}
	if b.s.Stopped || b.generation != 1 || b.executionIntent != 0 {
		t.Fatal("rejected detach changed lifecycle")
	}
}
