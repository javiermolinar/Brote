package vscode

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCancelledBuildStopsOwnedProcessGroup(t *testing.T) {
	root := t.TempDir()
	pidfile := filepath.Join(root, "pid")
	script := "#!/bin/sh\necho $$ > '" + pidfile + "'\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(root, "go"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- (&Launch{Program: root}).BuildContext(ctx, filepath.Join(root, "target")) }()
	var data []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		data, _ = os.ReadFile(pidfile)
		if len(data) > 0 {
			break
		}
	}
	if len(data) == 0 {
		t.Fatal("builder never started")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled build succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("build did not cancel")
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatal("owned builder survived cancellation")
	}
}
