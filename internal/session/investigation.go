package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Investigation metadata is separate from live descriptors and survives cleanup.
// Each run has its own immutable association, so concurrent brokers never rewrite
// a shared run list or lose one another's history.
type Investigation struct {
	RunOrdinals map[string]int `json:"runOrdinals,omitempty"`
	Created     string         `json:"created,omitempty"`
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Project     string         `json:"project"`
	Runs        []RunSummary   `json:"runs"`
}
type RunSummary struct {
	Ordinal         int    `json:"ordinal"`
	LaunchError     string `json:"launchError,omitempty"`
	LaunchAvailable bool   `json:"launchAvailable"`
	Summary
	Created     string `json:"created"`
	DebuggerPID int    `json:"debuggerPid,omitempty"`
	TargetPID   int    `json:"targetPid,omitempty"`
	Adapter     string `json:"adapter"`
	Protocol    string `json:"protocol"`
}

func investigationPath(id string) (string, error) {
	if !discussionID.MatchString(id) {
		return "", fmt.Errorf("invalid investigation ID")
	}
	root, e := DataRoot()
	return filepath.Join(root, "investigations", id+".json"), e
}
func InvestigationFor(id string) (string, error) {
	if !discussionID.MatchString(id) {
		return "", fmt.Errorf("invalid run ID")
	}
	root, e := DataRoot()
	if e != nil {
		return "", e
	}
	data, e := os.ReadFile(filepath.Join(root, "investigations", "runs", id+".json"))
	if os.IsNotExist(e) {
		return id, nil
	}
	if e != nil {
		return "", e
	}
	var group string
	e = json.Unmarshal(data, &group)
	if e != nil {
		return "", e
	}
	if !discussionID.MatchString(group) {
		return "", fmt.Errorf("invalid stored investigation")
	}
	return group, nil
}
func ReadInvestigation(id string) (Investigation, error) {
	var v Investigation
	p, e := investigationPath(id)
	if e != nil {
		return v, e
	}
	data, e := os.ReadFile(p)
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(data, &v)
	return v, e
}
func AssignInvestigation(run, group, title, project string) error {
	lock, err := lockInvestigations()
	if err != nil {
		return err
	}
	defer Unlock(lock)
	if !discussionID.MatchString(run) {
		return fmt.Errorf("invalid run ID")
	}
	if group == "" {
		group = run
	}
	p, e := investigationPath(group)
	if e != nil {
		return e
	}
	v, e := ReadInvestigation(group)
	if os.IsNotExist(e) {
		if group != run {
			return fmt.Errorf("investigation no longer exists")
		}
		v = Investigation{ID: group, Title: strings.TrimSpace(title), Project: project, Created: time.Now().UTC().Format(time.RFC3339Nano), RunOrdinals: map[string]int{}}
		if d, err := Read(run); err == nil {
			v.Created = d.Created
		}
		if v.Title == "" {
			v.Title = filepath.Base(project) + " investigation"
		}
		if len(v.Title) > 200 {
			return fmt.Errorf("title exceeds 200 bytes")
		}
		if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return e
		}
		if e = Write(p, v); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else {
		_, a := projectIdentity(v.Project)
		_, b := projectIdentity(project)
		if a != b {
			return fmt.Errorf("investigation belongs to another project")
		}
	}
	if v.RunOrdinals == nil {
		// Backfill existing legacy runs before reserving an ordinal for a new one.
		if _, err := workspaceLocked(context.Background()); err != nil {
			return err
		}
		if stored, err := ReadInvestigation(group); err == nil {
			v = stored
		}
	}
	reserveOrdinal(&v, run)
	v.Runs = nil
	if e = Write(p, v); e != nil {
		return e
	}
	root, e := DataRoot()
	if e != nil {
		return e
	}
	dir := filepath.Join(root, "investigations", "runs")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	return Write(filepath.Join(dir, run+".json"), group)
}
func Workspace(ctx context.Context) ([]Investigation, error) {
	lock, e := lockInvestigations()
	if e != nil {
		return nil, e
	}
	defer Unlock(lock)
	return workspaceLocked(ctx)
}
func workspaceLocked(ctx context.Context) ([]Investigation, error) {
	live, e := List(ctx)
	if e != nil {
		return nil, e
	}
	history, e := ListHistory()
	if e != nil {
		return nil, e
	}
	runs := map[string]RunSummary{}
	for _, h := range history {
		runs[h.ID] = RunSummary{Summary: Summary{ID: h.ID, Project: h.Project, Binary: h.Binary, Status: "ended"}, Created: h.Started, LaunchAvailable: true, Adapter: "Delve", Protocol: "DAP"}
		r := runs[h.ID]
		if dir, err := FindHistory(h.ID); err == nil {
			data, _ := os.ReadFile(filepath.Join(dir, "launch-error.json"))
			_ = json.Unmarshal(data, &r.LaunchError)
		}
		if h.Status != "ended" {
			r.Status = "offline"
		}
		runs[h.ID] = r
	}
	for _, s := range live {
		r := runs[s.ID]
		r.Summary = s
		r.Adapter = "Delve"
		r.Protocol = "DAP"
		if d, e := Read(s.ID); e == nil {
			r.Created = d.Created
			r.DebuggerPID = d.DelvePID
			r.TargetPID = d.TargetPID
			if d.Backend == "rpc" {
				r.Protocol = "JSON-RPC"
			}
		}
		runs[s.ID] = r
	}
	groups := map[string]*Investigation{}
	for _, r := range runs {
		group, e := InvestigationFor(r.ID)
		if e != nil {
			return nil, e
		}
		if groups[group] == nil {
			v, e := ReadInvestigation(group)
			if os.IsNotExist(e) {
				v = Investigation{ID: group, Title: filepath.Base(r.Binary), Project: r.Project}
				if discussion, e := ReadDiscussion(r.ID); e == nil && len(discussion.Threads) > 0 && len(discussion.Threads[0].Messages) > 0 {
					v.Title = discussion.Threads[0].Messages[0].Body
					if len(v.Title) > 100 {
						v.Title = v.Title[:100] + "…"
					}
				}
			} else if e != nil {
				return nil, e
			}
			v.Runs = []RunSummary{}
			groups[group] = &v
		}
		groups[group].Runs = append(groups[group].Runs, r)
	}
	result := []Investigation{}
	for _, v := range groups {
		sort.Slice(v.Runs, func(i, j int) bool {
			if v.Runs[i].Created == v.Runs[j].Created {
				return v.Runs[i].ID < v.Runs[j].ID
			}
			return v.Runs[i].Created < v.Runs[j].Created
		})
		changed := false
		for i := range v.Runs {
			if v.RunOrdinals[v.Runs[i].ID] == 0 {
				reserveOrdinal(v, v.Runs[i].ID)
				changed = true
			}
			v.Runs[i].Ordinal = v.RunOrdinals[v.Runs[i].ID]
		}
		if changed {
			p, err := investigationPath(v.ID)
			if err != nil {
				return nil, err
			}
			meta := *v
			meta.Runs = nil
			if err = Write(p, meta); err != nil {
				return nil, err
			}
		}
		if v.Created == "" {
			for _, r := range v.Runs {
				if _, err := time.Parse(time.RFC3339Nano, r.Created); err == nil {
					v.Created = r.Created
					break
				}
			}
			if v.Created != "" {
				p, e := investigationPath(v.ID)
				if e != nil {
					return nil, e
				}
				meta := *v
				meta.Runs = nil
				if e = Write(p, meta); e != nil {
					return nil, e
				}
			}
		}
		result = append(result, *v)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Runs[len(result[i].Runs)-1].Created > result[j].Runs[len(result[j].Runs)-1].Created
	})
	return result, nil
}

// SavedRun returns immutable recorded evidence. It never reads current source or
// contacts a debugger, and refuses untrusted snapshot paths from a damaged log.
func savedRunEvidence(id string) (map[string]any, error) {
	events, e := ReadHistory(id)
	if e != nil {
		return nil, e
	}
	dir, e := FindHistory(id)
	if e != nil {
		return nil, e
	}
	var meta HistoryMetadata
	data, e := os.ReadFile(filepath.Join(dir, "session.json"))
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(data, &meta); e != nil {
		return nil, e
	}
	ended := meta.Status == "ended"
	if descriptor, err := Read(id); err == nil && descriptor.Stopped {
		ended = true
	}
	out := map[string]any{"runEnded": ended, "id": id, "project": meta.Project, "binary": meta.Binary, "status": "exited", "historical": true, "state": map[string]any{}, "frame": 0, "generation": 0, "frames": []any{}}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "inspection.captured" {
			continue
		}
		var ref struct {
			Snapshot string `json:"snapshot"`
		}
		if json.Unmarshal(events[i].Data, &ref) != nil {
			continue
		}
		if filepath.Dir(ref.Snapshot) != "snapshots" || !regexp.MustCompile(`^[a-f0-9]{64}\.json$`).MatchString(filepath.Base(ref.Snapshot)) {
			return nil, fmt.Errorf("invalid snapshot reference")
		}
		path := filepath.Join(dir, ref.Snapshot)
		data, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		var snap struct {
			Source   any `json:"source"`
			Contexts []struct {
				ID            string `json:"id"`
				SelectedFrame *int   `json:"selected_frame"`
				Frames        []struct {
					Function string `json:"function"`
					File     string `json:"file"`
					Line     int    `json:"line"`
				} `json:"frames"`
				Observations []struct {
					Frame int    `json:"frame_index"`
					Scope string `json:"scope"`
					Value any    `json:"value"`
				} `json:"observations"`
			} `json:"contexts"`
		}
		if e = json.Unmarshal(data, &snap); e != nil {
			return nil, e
		}
		out["source"] = snap.Source
		out["capturedAt"] = events[i].At
		if len(snap.Contexts) > 0 {
			c := snap.Contexts[0]
			var contextID int
			fmt.Sscan(c.ID, &contextID)
			if contextID > 0 {
				out["goroutine"] = contextID
				out["goroutines"] = []map[string]any{{"id": contextID}}
			}
			selected := 0
			if c.SelectedFrame != nil {
				selected = *c.SelectedFrame
			} else if len(c.Observations) > 0 {
				selected = c.Observations[0].Frame
			}
			if selected >= 0 && selected < len(c.Frames) {
				out["frame"] = selected
			}
			frames := []map[string]any{}
			for _, f := range c.Frames {
				frames = append(frames, map[string]any{"file": f.File, "line": f.Line, "function": map[string]any{"name": f.Function}})
			}
			for _, o := range c.Observations {
				if o.Frame >= 0 && o.Frame < len(frames) && (o.Scope == "Locals" || o.Scope == "Arguments") {
					values, _ := frames[o.Frame][o.Scope].([]any)
					frames[o.Frame][o.Scope] = append(values, o.Value)
				}
			}
			out["frames"] = frames
		}
		break
	}
	d, e := ReadDiscussion(id)
	if e != nil {
		return nil, e
	}
	out["discussion"] = d
	return out, nil
}

func lockInvestigations() (*os.File, error) {
	root, e := DataRoot()
	if e != nil {
		return nil, e
	}
	dir := filepath.Join(root, "investigations")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	return Lock(dir)
}

// SavedRun preserves metadata and discussions when a legacy run has no archive.
func SavedRun(id string) (map[string]any, error) {
	out, err := savedRunEvidence(id)
	if err == nil {
		return out, nil
	}
	if !discussionID.MatchString(id) {
		return nil, err
	}
	var meta HistoryMetadata
	if dir, e := FindHistory(id); e == nil {
		data, e := os.ReadFile(filepath.Join(dir, "session.json"))
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(data, &meta); e != nil {
			return nil, e
		}
	} else if d, e := Read(id); e == nil {
		meta = HistoryMetadata{ID: id, Project: d.Project, Binary: d.Binary}
		if d.Stopped {
			meta.Status = "ended"
		}
	} else if discussion, e := ReadDiscussion(id); e == nil && len(discussion.Threads) > 0 {
		meta = HistoryMetadata{ID: id, Project: discussion.Project, Binary: discussion.Binary, Status: "ended"}
	} else {
		return nil, err
	}
	d, e := ReadDiscussion(id)
	if e != nil {
		return nil, e
	}
	return map[string]any{"id": id, "project": meta.Project, "binary": meta.Binary, "status": "exited", "historical": true, "runEnded": meta.Status == "ended", "state": map[string]any{}, "frame": 0, "frames": []any{}, "discussion": d, "snapshotUnavailable": true}, nil
}

// RecordFailedLaunch is only called by the launcher before starting a child, or
// by the broker after its startup cleanup. A live descriptor is never overwritten.
func RecordFailedLaunch(d Descriptor, args []string, cause string) error {
	lock, err := lockInvestigations()
	if err != nil {
		return err
	}
	defer Unlock(lock)
	if _, err = Read(d.ID); err == nil {
		return fmt.Errorf("run already published; recover it before cleanup")
	} else if !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
		return err
	}
	h, err := OpenHistory(d, args)
	if err != nil {
		return err
	}
	defer h.Close()
	if err = Write(filepath.Join(h.Dir, "launch-error.json"), cause); err != nil {
		return err
	}
	if err = h.Append("launch.failed", "system", map[string]any{"error": cause}); err != nil {
		return err
	}
	err = h.Append("session.ended", "system", map[string]any{"reason": "launch failed"})
	return err
}

func reserveOrdinal(v *Investigation, id string) {
	if v.RunOrdinals == nil {
		v.RunOrdinals = map[string]int{}
	}
	if v.RunOrdinals[id] != 0 {
		return
	}
	n := 0
	for _, ordinal := range v.RunOrdinals {
		if ordinal > n {
			n = ordinal
		}
	}
	v.RunOrdinals[id] = n + 1
}
