package session

import (
	"context"
	"testing"
)

func TestFailedLaunchRemainsVisibleAndDeletable(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	d := Descriptor{ID: "1111111111", Project: t.TempDir(), Binary: "/missing", Created: "2026-09-21T10:00:00Z"}
	if err := AssignInvestigation(d.ID, "", "Failed launch", d.Project); err != nil {
		t.Fatal(err)
	}
	if err := RecordFailedLaunch(d, nil, "child could not start"); err != nil {
		t.Fatal(err)
	}
	groups, err := Workspace(context.Background())
	if err != nil || len(groups) != 1 || len(groups[0].Runs) != 1 {
		t.Fatalf("%+v %v", groups, err)
	}
	if groups[0].Runs[0].Status != "ended" || groups[0].Runs[0].LaunchError != "child could not start" {
		t.Fatal(groups[0].Runs)
	}
	if err = DeleteInvestigation(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
}
