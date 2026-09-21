package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInvestigationGroupingSurvivesRuntimeCleanup(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	project := t.TempDir()
	for _, id := range []string{"1111111111", "2222222222"} {
		s := Descriptor{ID: id, Project: project, Binary: "/tmp/demo", Created: "2026-09-21T10:00:00Z"}
		h, e := OpenHistory(s, []string{"arg"})
		if e != nil {
			t.Fatal(e)
		}
		if e = h.Append("session.ended", "human", nil); e != nil {
			t.Fatal(e)
		}
		h.Close()
		if e = AssignInvestigation(id, "1111111111", "Investigate total", project); e != nil {
			t.Fatal(e)
		}
	}
	list, e := Workspace(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(list) != 1 || len(list[0].Runs) != 2 || list[0].Title != "Investigate total" || list[0].Runs[0].Project != project {
		t.Fatalf("wrong grouping: %+v", list)
	}
	if e = AssignInvestigation("3333333333", "1111111111", "", t.TempDir()); e == nil {
		t.Fatal("cross-project run accepted")
	}
	for _, id := range []string{"../escape", "", "nothex"} {
		if _, e := InvestigationFor(id); e == nil {
			t.Fatal("invalid ID accepted")
		}
	}
}
func TestSavedRunUsesCapturedSource(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	project := t.TempDir()
	s := Descriptor{ID: "1111111111", Project: project, Binary: "/tmp/demo", Created: "2026-09-21T10:00:00Z"}
	h, e := OpenHistory(s, nil)
	if e != nil {
		t.Fatal(e)
	}
	ref, e := h.Snapshot(map[string]any{"source": map[string]any{"file": "/deleted/main.go", "line": 4, "start": 4, "lines": []string{"captured := 42"}}, "contexts": []any{map[string]any{"frames": []any{map[string]any{"function": "main.main", "file": "/deleted/main.go", "line": 4}}, "observations": []any{map[string]any{"frame_index": 0, "scope": "Locals", "value": map[string]any{"name": "value", "value": "42"}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	h.Append("inspection.captured", "observer", map[string]any{"snapshot": ref})
	h.Close()
	result, e := SavedRun(s.ID)
	if e != nil {
		t.Fatal(e)
	}
	if result["project"] != project || result["historical"] != true {
		t.Fatal(result)
	}
	source := result["source"].(map[string]any)
	if source["lines"].([]any)[0] != "captured := 42" {
		t.Fatal(source)
	}
	os.Remove(filepath.Join(h.Dir, ref))
	if result, e = SavedRun(s.ID); e != nil || result["snapshotUnavailable"] != true || result["source"] != nil {
		t.Fatal("missing evidence must be explicit", result, e)
	}
}

func TestCreationDateSurvivesFirstRunDeletion(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	project := t.TempDir()
	for _, id := range []string{"1111111111", "2222222222"} {
		s := Descriptor{ID: id, Project: project, Binary: "/demo", Created: "2026-09-20T10:00:00Z"}
		h, e := OpenHistory(s, nil)
		if e != nil {
			t.Fatal(e)
		}
		h.Append("session.ended", "human", nil)
		h.Close()
	}
	root, _ := DataRoot()
	os.MkdirAll(filepath.Join(root, "investigations", "runs"), 0700)
	Write(filepath.Join(root, "investigations", "runs", "2222222222.json"), "1111111111")
	if e := DeleteRun("1111111111"); e != nil {
		t.Fatal(e)
	}
	groups, e := Workspace(context.Background())
	if e != nil || len(groups) != 1 || groups[0].Created != "2026-09-20T10:00:00Z" {
		t.Fatalf("%+v %v", groups, e)
	}
	meta, e := ReadInvestigation("1111111111")
	if e != nil || meta.Created != groups[0].Created {
		t.Fatal(meta, e)
	}
}

func TestRunOrdinalsSurviveDeletionAndRerun(t *testing.T) {
	t.Setenv("AGENTDEBUGGER_DATA_DIR", t.TempDir())
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	project := t.TempDir()
	for _, id := range []string{"1111111111", "2222222222", "3333333333"} {
		d := Descriptor{ID: id, Project: project, Binary: "/demo", Created: "2026-09-21T10:00:00Z"}
		h, err := OpenHistory(d, nil)
		if err != nil {
			t.Fatal(err)
		}
		h.Append("session.ended", "human", nil)
		h.Close()
		if err = AssignInvestigation(id, "1111111111", "Demo", project); err != nil {
			t.Fatal(err)
		}
	}
	before, err := Workspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before[0].Runs[2].Ordinal != 3 {
		t.Fatal(before)
	}
	if err = DeleteRun("1111111111"); err != nil {
		t.Fatal(err)
	}
	after, err := Workspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after[0].Runs[0].Ordinal != 2 || after[0].Runs[1].Ordinal != 3 {
		t.Fatal(after)
	}
	if err = AssignInvestigation("4444444444", "1111111111", "Demo", project); err != nil {
		t.Fatal(err)
	}
	group, err := ReadInvestigation("1111111111")
	if err != nil || group.RunOrdinals["4444444444"] != 4 {
		t.Fatalf("%+v %v", group, err)
	}
}
