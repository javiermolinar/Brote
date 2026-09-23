package vscode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func validateBuildFlags(flags []string) error {
	for _, flag := range flags {
		if !strings.HasPrefix(flag, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(flag, "-"), "=")
		switch strings.TrimPrefix(name, "-") {
		case "o", "C", "gcflags", "c", "args":
			return fmt.Errorf("buildFlags cannot override -%s; Brote manages the build output and debug symbols", name)
		}
		if flag == "--" {
			return fmt.Errorf("buildFlags cannot contain --")
		}
	}
	return nil
}

// Build creates a session-owned executable. The CLI requires --build before
// invoking this; handovers, recovery and run-again never rebuild a target.
func (l *Launch) Build(output string) error { return l.BuildContext(context.Background(), output) }

func (l *Launch) BuildContext(ctx context.Context, output string) error {
	st, err := os.Stat(l.Program)
	if err != nil {
		return err
	}
	dir, target := l.Program, "."
	if !st.IsDir() {
		dir, target = filepath.Dir(l.Program), "./"+filepath.Base(l.Program)
	}
	args := []string{"build"}
	if l.Mode == "test" {
		args = []string{"test", "-c"}
	}
	args = append(args, l.BuildFlags...)
	args = append(args, "-o", output, "-gcflags=all=-N -l", target)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Dir = dir
	cmd.Env = l.Settings.Environment(cmd.Environ())
	if data, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("building launch configuration %q: %w\n%s", l.Name, err, strings.TrimSpace(string(data)))
	}
	if st, err := os.Stat(output); err != nil || st.IsDir() || st.Mode()&0111 == 0 {
		return fmt.Errorf("launch configuration %q did not produce an executable", l.Name)
	}
	return nil
}
