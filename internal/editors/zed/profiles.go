package zed

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"debug-handover/internal/session"
)

func Label(id string) string { return "Debug Handover · " + id }

// WriteConfig updates only this session's attach profile, preserving other entries.
func WriteConfig(s session.Descriptor) (string, error) {
	host, port, e := net.SplitHostPort(s.DAP)
	if e != nil {
		return "", e
	}
	n, e := strconv.Atoi(port)
	if e != nil {
		return "", e
	}
	profile := obj{"label": Label(s.ID), "adapter": "Delve", "request": "attach", "mode": "remote", "stopOnEntry": true, "tcp_connection": obj{"host": host, "port": n}}
	path := filepath.Join(s.Project, ".zed", "debug.json")
	data, e := os.ReadFile(path)
	mode := os.FileMode(0644)
	if os.IsNotExist(e) {
		data = []byte("[\n]\n")
	} else if e != nil {
		return "", e
	} else if st, e := os.Stat(path); e == nil {
		mode = st.Mode().Perm()
	}
	updated, e := appendZedProfile(data, profile)
	if e != nil {
		return "", e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return "", e
	}
	// Use a unique adjacent temporary file so a failed write cannot truncate config.
	f, e := os.CreateTemp(filepath.Dir(path), ".debug-handover-*")
	if e != nil {
		return "", e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(updated)
	}
	closeErr := f.Close()
	if e != nil {
		return "", e
	}
	if closeErr != nil {
		return "", closeErr
	}
	if current, e := os.ReadFile(path); e == nil && string(current) != string(data) {
		return "", fmt.Errorf("Zed config changed while preparing handover; retry")
	}
	return path, os.Rename(tmp, path)
}

// HasProfile reports whether this session already has an editor profile.
func HasProfile(s session.Descriptor) bool {
	data, e := os.ReadFile(filepath.Join(s.Project, ".zed", "debug.json"))
	if e != nil {
		return false
	}
	start, _, e := zedProfileRange(data, Label(s.ID))
	return e == nil && start >= 0
}

func RemoveConfig(s session.Descriptor) error {
	path := filepath.Join(s.Project, ".zed", "debug.json")
	data, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	updated, e := removeZedProfile(data, Label(s.ID))
	if e != nil {
		return e
	}
	if string(updated) == string(data) {
		return nil
	}
	st, e := os.Stat(path)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".debug-handover-*")
	if e != nil {
		return e
	}
	temp := f.Name()
	defer os.Remove(temp)
	if e = f.Chmod(st.Mode().Perm()); e == nil {
		_, e = f.Write(updated)
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	current, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if string(current) != string(data) {
		return fmt.Errorf("Zed config changed during cleanup; retry")
	}
	return os.Rename(temp, path)
}
