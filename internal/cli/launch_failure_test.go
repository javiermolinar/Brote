package cli

import (
	"agentdebugger/internal/session"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeDirectoryFailureIsRecorded(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "blocked")
	os.WriteFile(root, []byte("file"), 0600)
	t.Setenv("DEBUG_HANDOVER_HOME", root)
	exe, _ := os.Executable()
	if _, err := start([]string{"--thread", "", "--project", t.TempDir(), "--binary", exe, "--dlv", exe}); err == nil {
		t.Fatal("expected directory failure")
	}
	os.Remove(root)
	os.MkdirAll(root, 0700)
	groups, err := session.Workspace(context.Background())
	if err != nil || len(groups) != 1 || groups[0].Runs[0].LaunchError == "" {
		t.Fatalf("missing failed launch: %+v %v", groups, err)
	}
	if err = session.DeleteInvestigation(context.Background(), groups[0].ID); err != nil {
		t.Fatal(err)
	}
}
