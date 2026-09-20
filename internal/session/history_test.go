package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func historyFixture(t *testing.T) Descriptor {
	t.Helper()
	root := t.TempDir()
	t.Setenv("AGENTDEBUGGER_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(root, "runtime"))
	project := filepath.Join(root, "tempo")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	return Descriptor{ID: "0123456789", Project: project, Binary: filepath.Join(project, "demo"), Created: "2026-09-20T01:00:00+02:00"}
}
func TestHistoryRecoveryAndOffline(t *testing.T) {
	s := historyFixture(t)
	h, err := OpenHistory(s, []string{"--demo"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("projects", "tempo", "2026-09-19", s.ID)
	if !strings.HasSuffix(h.Dir, want) {
		t.Fatalf("directory %s, want suffix %s", h.Dir, want)
	}
	if _, err := OpenHistory(s, nil); err == nil {
		t.Fatal("allowed second writer")
	}
	value := map[string]any{"v": 1, "stop_id": "stop-1", "contexts": []any{map[string]any{"id": "1", "kind": "task"}}}
	ref, err := h.Snapshot(value)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.Snapshot(value)
	if err != nil || again != ref {
		t.Fatal("snapshot not content addressed", again, err)
	}
	if err = h.Append("inspection.captured", "human", map[string]any{"snapshot": ref}); err != nil {
		t.Fatal(err)
	}
	dir := h.Dir
	h.Close()
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"v":1,"seq":3`)
	f.Close()
	events, err := ReadHistory(s.ID)
	if err != nil || len(events) != 2 {
		t.Fatalf("incomplete tail: %v %v", events, err)
	}
	h, err = OpenHistory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Append("session.ended", "debugger", map[string]any{"reason": "exit"}); err != nil {
		t.Fatal(err)
	}
	if err = h.Append("broker.disconnected", "core", nil); err != nil {
		t.Fatal(err)
	}
	h.Close()
	if err = os.RemoveAll(Root()); err != nil {
		t.Fatal(err)
	}
	events, err = ReadHistory(s.ID)
	if err != nil || len(events) != 4 {
		t.Fatalf("offline read %v %v", events, err)
	}
	for i, event := range events {
		if event.Seq != uint64(i+1) {
			t.Fatal("sequence gap")
		}
	}
	list, err := ListHistory()
	if err != nil || len(list) != 1 || list[0].Status != "ended" {
		t.Fatalf("catalog %v %v", list, err)
	}
	if len(list[0].Args) != 1 {
		t.Fatal("lost original launch arguments")
	}
	// Rebuild a stale summary using the journal lifecycle.
	list[0].Status = "active"
	if err = Write(filepath.Join(dir, "session.json"), list[0]); err != nil {
		t.Fatal(err)
	}
	h, err = OpenHistory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if h.Metadata.Status != "ended" {
		t.Fatal("did not recover summary")
	}
}
func TestHistoryRejectsCorruptCompleteRecord(t *testing.T) {
	s := historyFixture(t)
	h, err := OpenHistory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.Dir, "events.jsonl")
	h.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString("broken\n")
	f.Close()
	before, _ := os.ReadFile(path)
	if _, err = OpenHistory(s, nil); err == nil {
		t.Fatal("accepted corrupt completed record")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("silently truncated corruption")
	}
}
func TestHistoryProjectCollisionsAndWorktrees(t *testing.T) {
	s := historyFixture(t)
	h, err := OpenHistory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := h.Dir
	h.Close()
	s.Project = filepath.Join(t.TempDir(), "tempo")
	os.MkdirAll(s.Project, 0700)
	s.ID = "1123456789"
	h, err = OpenHistory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h.Dir == first || !strings.Contains(h.Dir, "tempo-") {
		t.Fatal("project collision", h.Dir)
	}
	h.Close()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init", s.Project)
	git("-C", s.Project, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(t.TempDir(), "other-name")
	git("-C", s.Project, "worktree", "add", "--detach", worktree)
	n1, i1 := projectIdentity(s.Project)
	n2, i2 := projectIdentity(worktree)
	if n1 != n2 || i1 != i2 {
		t.Fatalf("worktrees differ: %s %s / %s %s", n1, i1, n2, i2)
	}
}
func TestHistoryDiscussionMigration(t *testing.T) {
	s := historyFixture(t)
	d := Discussion{Session: s.ID, Project: s.Project, Threads: []CommentThread{{ID: "thread-1", Messages: []CommentMessage{{ID: "message-1", Body: "question"}}}}}
	if err := WriteDiscussion(d); err != nil {
		t.Fatal(err)
	}
	h, err := OpenHistory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	old, err := ReadDiscussion(s.ID)
	if err != nil || old.Threads[0].ID != "thread-1" {
		t.Fatal("legacy read failed", err)
	}
	if err = Write(filepath.Join(h.Dir, "discussion.json"), old); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(Root())
	got, err := ReadDiscussion(s.ID)
	if err != nil || len(got.Threads) != 1 {
		t.Fatal("archive discussion missing", err)
	}
	data, _ := json.Marshal(got)
	if !strings.Contains(string(data), "message-1") {
		t.Fatal("lost message ID")
	}
}
