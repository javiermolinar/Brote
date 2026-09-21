package broker

import (
	"agentdebugger/internal/session"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestChildStartupFailureFinalizesInvestigation(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	project := t.TempDir()
	id := "1111111111"
	if err := session.AssignInvestigation(id, "", "Failure", project); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(session.Root(), id), 0700)
	if err := Serve(Options{ID: id, Project: project, Binary: "/missing", Delve: "/missing-delvex"}); err == nil {
		t.Fatal("expected start failure")
	}
	groups, err := session.Workspace(context.Background())
	if err != nil || len(groups) != 1 || groups[0].Runs[0].LaunchError == "" {
		t.Fatalf("%+v %v", groups, err)
	}
	if err = session.DeleteInvestigation(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
