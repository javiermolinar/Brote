package cli

import (
	"agentdebugger/internal/session"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalStepAndLegacyPayloads(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", root)
	var body obj
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(obj{"id": "s", "run": "r", "generation": 7})
			return
		}
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(obj{"status": "accepted"})
	}))
	defer server.Close()
	os.MkdirAll(filepath.Join(root, "s"), 0700)
	if err := session.Write(filepath.Join(root, "s", "session.json"), session.Descriptor{ID: "s", HTTP: server.URL, RunID: "r", ServiceVersion: 1, Token: "secret", Binding: &session.Binding{ID: "b", Revision: 2}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args      []string
		operation string
	}{
		{[]string{"debug", "step", "s"}, "next"}, {[]string{"debug", "step", "s", "--over"}, "next"}, {[]string{"debug", "step", "s", "--into"}, "step"}, {[]string{"debug", "step", "s", "--out"}, "stepout"}, {[]string{"step", "s"}, "step"},
	} {
		if _, err := Run(append(tc.args, "--task", "task", "--command-id", "fixed")); err != nil {
			t.Fatal(err)
		}
		if body["action"] != tc.operation || body["actor"] != "agent" || body["task"] != "task" || body["binding"] != "b" || body["generation"] != float64(7) || body["run"] != "r" || body["commandId"] != "fixed" {
			t.Fatal(body)
		}
	}
}
func TestCanonicalFlagsRejectBeforeSessionRead(t *testing.T) {
	for _, args := range [][]string{{"debug", "step", "missing", "--over", "--into"}, {"debug", "state", "missing", "--instruction", "x"}, {"debug", "breakpoint", "list", "missing", "--file", "x"}, {"debug", "stack", "missing", "--expression", "x"}, {"debug", "capabilities", "missing", "--count", "1"}, {"comment", "status", "missing", "thread", "open", "--context", "current"}} {
		_, err := Run(args)
		if err == nil || strings.Contains(err.Error(), "no such file") {
			t.Fatal(args, err)
		}
	}
	if _, _, err := parseAction("state", []string{"--instruction", "legacy"}, true); err != nil {
		t.Fatal("legacy flags rejected", err)
	}
}
func TestCanonicalCommentStatusOffline(t *testing.T) {
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	d := session.Discussion{Session: "0123456789", Threads: []session.CommentThread{{ID: "thread"}}}
	if err := session.CommitDiscussion(&d); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"resolved", "open"} {
		if _, err := Run([]string{"comment", "status", d.Session, "thread", status, "--offline"}); err != nil {
			t.Fatal(err)
		}
		got, err := session.ReadDiscussion(d.Session)
		if err != nil || got.Threads[0].Resolved != (status == "resolved") {
			t.Fatal(got, err)
		}
	}
	if _, err := session.Read(d.Session); err == nil {
		t.Fatal("offline status created a target")
	}
}

func TestCanonicalCommandsRejectExtraArguments(t *testing.T) {
	for _, args := range [][]string{{"system", "version", "extra"}, {"system", "doctor", "extra"}, {"session", "events", "watch", "missing", "extra"}, {"session", "events", "wait", "missing", "extra"}} {
		if _, err := Run(args); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
			t.Fatal(args, err)
		}
	}
}

func TestCanonicalEventModeFlags(t *testing.T) {
	for _, args := range [][]string{{"session", "events", "watch", "missing", "--timeout", "100ms"}, {"session", "events", "wait", "missing", "--managed"}, {"session", "events", "wait", "missing", "--consumer", "host"}, {"session", "events", "watch", "missing", "--consumer", "host"}, {"session", "events", "watch", "missing", "--managed", "--consumer", "host", "--cursor", "1"}} {
		if _, err := Run(args); err == nil || !strings.Contains(err.Error(), "not valid for this operation") {
			t.Fatal(args, err)
		}
	}
	for _, mode := range []string{"watch", "wait"} {
		var out bytes.Buffer
		if _, err := runCLI([]string{"session", "events", mode, "--help"}, &out); err != nil {
			t.Fatal(err)
		}
		if (mode == "watch" && strings.Contains(out.String(), "--timeout")) || (mode == "wait" && (strings.Contains(out.String(), "--managed") || strings.Contains(out.String(), "--consumer"))) {
			t.Fatal(out.String())
		}
	}
}
