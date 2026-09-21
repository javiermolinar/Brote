package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type deletion struct {
	paths []string
	locks []*os.File
}

func (d *deletion) close() {
	for _, l := range d.locks {
		Unlock(l)
	}
}
func (d *deletion) prepare(id string) error {
	if !discussionID.MatchString(id) {
		return fmt.Errorf("invalid run ID")
	}
	descriptor, e := Read(id)
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if exists && !descriptor.Stopped {
		return fmt.Errorf("end run %s before deleting it", id)
	}
	runtimeDir := filepath.Join(Root(), id)
	if exists {
		l, e := Lock(runtimeDir)
		if e != nil {
			return e
		}
		d.locks = append(d.locks, l)
	}
	archive, e := FindHistory(id)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e == nil {
		l, e := Lock(archive)
		if e != nil {
			return e
		}
		d.locks = append(d.locks, l)
		data, e := os.ReadFile(filepath.Join(archive, "session.json"))
		if e != nil {
			return e
		}
		var meta HistoryMetadata
		if e = json.Unmarshal(data, &meta); e != nil {
			return e
		}
		if !exists && meta.Status != "ended" {
			return fmt.Errorf("end run %s before deleting it", id)
		}
		d.paths = append(d.paths, archive)
	} else if !exists {
		return fmt.Errorf("run %s has no verified ended record; it may still be starting", id)
	}
	legacy := os.Getenv("DEBUG_HANDOVER_HOME")
	if legacy != "" {
		legacy = filepath.Join(legacy, "discussions")
	} else {
		config, e := os.UserConfigDir()
		if e != nil {
			return e
		}
		legacy = filepath.Join(config, "delve-llm-adapter", "discussions")
	}
	root, e := DataRoot()
	if e != nil {
		return e
	}
	d.paths = append(d.paths, runtimeDir, filepath.Join(legacy, id+".json"), filepath.Join(root, "investigations", "runs", id+".json"))
	return nil
}

// Stage renames first. A failed rename restores earlier paths before any erase.
// A cleanup failure leaves hidden staged files, reported explicitly to callers.
func (d *deletion) commit() error {
	type moved struct{ from, to string }
	var staged []moved
	var paths []string
	for _, p := range d.paths {
		if _, e := os.Lstat(p); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		paths = append(paths, p)
	}
	for _, p := range paths {
		to := filepath.Join(filepath.Dir(p), ".deleted-"+NewID(8))
		if e := os.Rename(p, to); e != nil {
			var rollback error
			for i := len(staged) - 1; i >= 0; i-- {
				if err := os.Rename(staged[i].to, staged[i].from); err != nil {
					rollback = err
				}
			}
			return fmt.Errorf("deletion not completed: %v (restore error: %v)", e, rollback)
		}
		staged = append(staged, moved{p, to})
	}
	for _, p := range staged {
		if e := os.RemoveAll(p.to); e != nil {
			return fmt.Errorf("records removed from the workspace but cleanup incomplete at %s: %w", p.to, e)
		}
	}
	return nil
}
func DeleteRun(id string) error {
	lock, e := lockInvestigations()
	if e != nil {
		return e
	}
	defer Unlock(lock)
	if _, e = workspaceLocked(context.Background()); e != nil {
		return e
	}
	d := deletion{}
	defer d.close()
	if e = d.prepare(id); e != nil {
		return e
	}
	return d.commit()
}
func DeleteInvestigation(ctx context.Context, id string) error {
	if !discussionID.MatchString(id) {
		return fmt.Errorf("invalid investigation ID")
	}
	lock, e := lockInvestigations()
	if e != nil {
		return e
	}
	defer Unlock(lock)
	groups, e := workspaceLocked(ctx)
	if e != nil {
		return e
	}
	ids := map[string]bool{}
	for _, g := range groups {
		if g.ID == id {
			for _, r := range g.Runs {
				ids[r.ID] = true
			}
		}
	}
	root, e := DataRoot()
	if e != nil {
		return e
	}
	// Associations include launches not yet published as a session descriptor.
	entries, e := filepath.Glob(filepath.Join(root, "investigations", "runs", "*.json"))
	if e != nil {
		return e
	}
	for _, p := range entries {
		data, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		var group string
		if e = json.Unmarshal(data, &group); e != nil {
			return e
		}
		if group == id {
			ids[strings.TrimSuffix(filepath.Base(p), ".json")] = true
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("investigation not found")
	}
	d := deletion{}
	defer d.close()
	for run := range ids {
		if e = d.prepare(run); e != nil {
			return e
		}
	}
	p, e := investigationPath(id)
	if e != nil {
		return e
	}
	d.paths = append(d.paths, p)
	return d.commit()
}
