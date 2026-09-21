package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRun(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	project := t.TempDir()
	for _, id := range []string{"1111111111", "2222222222"} {
		d := Descriptor{ID: id, Project: project, Binary: "/demo", Created: "2026-09-21T10:00:00Z"}
		h, e := OpenHistory(d, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = DeleteRun(id); e == nil {
			t.Fatal("deleted active history")
		}
		h.Append("session.ended", "human", nil)
		h.Close()
		if e = AssignInvestigation(id, "1111111111", "Demo", project); e != nil {
			t.Fatal(e)
		}
	}
	if e := DeleteRun("../escape"); e == nil {
		t.Fatal("accepted invalid ID")
	}
	if e := DeleteRun("1111111111"); e != nil {
		t.Fatal(e)
	}
	if _, e := FindHistory("1111111111"); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	groups, e := Workspace(context.Background())
	if e != nil || len(groups) != 1 || len(groups[0].Runs) != 1 || groups[0].Runs[0].ID != "2222222222" {
		t.Fatalf("remaining runs: %+v %v", groups, e)
	}
	id := "3333333333"
	dir := filepath.Join(Root(), id)
	os.MkdirAll(dir, 0700)
	d := Descriptor{ID: id, Stopped: false}
	Write(filepath.Join(dir, "session.json"), d)
	if e := DeleteRun(id); e == nil {
		t.Fatal("deleted unended descriptor")
	}
	d.Stopped = true
	Write(filepath.Join(dir, "session.json"), d)
	if e := DeleteRun(id); e != nil {
		t.Fatal(e)
	}
	if _, e := Read(id); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

func TestDeleteInvestigationPreflightAndPendingLaunch(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	project := t.TempDir()
	for _, id := range []string{"1111111111", "2222222222", "3333333333"} {
		h, e := OpenHistory(Descriptor{ID: id, Project: project, Created: "2026-09-20T10:00:00Z"}, nil)
		if e != nil {
			t.Fatal(e)
		}
		h.Append("session.ended", "human", nil)
		h.Close()
		group := "1111111111"
		if id == "3333333333" {
			group = id
		}
		if e := AssignInvestigation(id, group, "Demo", project); e != nil {
			t.Fatal(e)
		}
	}
	dir := filepath.Join(Root(), "2222222222")
	os.MkdirAll(dir, 0700)
	Write(filepath.Join(dir, "session.json"), Descriptor{ID: "2222222222"})
	if e := DeleteInvestigation(context.Background(), "1111111111"); e == nil {
		t.Fatal("active member accepted")
	}
	if _, e := FindHistory("1111111111"); e != nil {
		t.Fatal("partially deleted", e)
	}
	Write(filepath.Join(dir, "session.json"), Descriptor{ID: "2222222222", Stopped: true})
	if e := AssignInvestigation("4444444444", "1111111111", "", project); e != nil {
		t.Fatal(e)
	}
	if e := DeleteInvestigation(context.Background(), "1111111111"); e == nil {
		t.Fatal("pending launch accepted")
	}
	root, _ := DataRoot()
	os.Remove(filepath.Join(root, "investigations", "runs", "4444444444.json"))
	if e := DeleteInvestigation(context.Background(), "1111111111"); e != nil {
		t.Fatal(e)
	}
	if _, e := FindHistory("1111111111"); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if _, e := FindHistory("2222222222"); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if _, e := FindHistory("3333333333"); e != nil {
		t.Fatal("unrelated run removed", e)
	}
	if e := AssignInvestigation("5555555555", "1111111111", "", project); e == nil {
		t.Fatal("deleted investigation recreated by launch")
	}
}

func TestDeleteInvestigationLockedMemberLeavesAllEvidence(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	project := t.TempDir()
	for _, id := range []string{"1111111111", "2222222222"} {
		h, e := OpenHistory(Descriptor{ID: id, Project: project, Created: "2026-09-20T10:00:00Z"}, nil)
		if e != nil {
			t.Fatal(e)
		}
		h.Append("session.ended", "human", nil)
		h.Close()
		AssignInvestigation(id, "1111111111", "Demo", project)
	}
	dir, _ := FindHistory("2222222222")
	lock, e := Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = DeleteInvestigation(context.Background(), "1111111111"); e == nil {
		t.Fatal("locked member deleted")
	}
	Unlock(lock)
	for _, id := range []string{"1111111111", "2222222222"} {
		if _, e = FindHistory(id); e != nil {
			t.Fatal(e)
		}
	}
	if e = DeleteInvestigation(context.Background(), "1111111111"); e != nil {
		t.Fatal("retry failed", e)
	}
}
