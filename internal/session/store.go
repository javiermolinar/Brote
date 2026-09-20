package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func Write(path string, value any) error {
	data, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".handover-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}

func Lock(dir string) (*os.File, error) {
	f, e := os.OpenFile(filepath.Join(dir, "broker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("session already has a broker or maintenance operation")
	}
	return f, nil
}

func Unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }

func ProcessExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	e := syscall.Kill(pid, 0)
	return e == nil || errors.Is(e, syscall.EPERM)
}
