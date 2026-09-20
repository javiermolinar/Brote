package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"debug-handover/internal/agents/codex"
	"debug-handover/internal/session"
)

func start(args []string) (obj, error) {
	f := flag.NewFlagSet("start", flag.ContinueOnError)
	bin := f.String("binary", "", "Existing Go executable or test binary")
	project := f.String("project", ".", "Project/source directory")
	dlv := f.String("dlv", "dlv", "Delve executable")
	thread := f.String("thread", os.Getenv("CODEX_THREAD_ID"), "Codex task for inspector handovers; empty disables wakeups")
	if e := f.Parse(args); e != nil {
		return nil, e
	}
	if *bin == "" {
		return nil, fmt.Errorf("--binary is required; build once with go build -gcflags='all=-N -l'")
	}
	if *thread != "" && !codex.ValidThread(*thread) {
		return nil, fmt.Errorf("--thread must be a Codex task UUID")
	}
	abs, e := filepath.Abs(*bin)
	if e != nil {
		return nil, e
	}
	st, e := os.Stat(abs)
	if e != nil {
		return nil, e
	}
	if st.IsDir() || st.Mode()&0111 == 0 {
		return nil, fmt.Errorf("binary is not executable: %s", abs)
	}
	root, e := filepath.Abs(*project)
	if e != nil {
		return nil, e
	}
	st, e = os.Stat(root)
	if e != nil || !st.IsDir() {
		return nil, fmt.Errorf("project directory does not exist: %s", root)
	}
	delve, e := exec.LookPath(*dlv)
	if e != nil && *dlv == "dlv" {
		home, _ := os.UserHomeDir()
		delve, e = exec.LookPath(filepath.Join(home, "go", "bin", "dlv"))
	}
	if e != nil {
		return nil, e
	}
	id := session.NewID(5)
	dir := filepath.Join(session.Root(), id)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	log, e := os.OpenFile(filepath.Join(dir, "broker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return nil, e
	}
	defer log.Close()
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	childArgs := []string{"serve", "--id", id, "--binary", abs, "--project", root, "--dlv", delve, "--thread", *thread, "--"}
	childArgs = append(childArgs, f.Args()...)
	cmd := exec.Command(exe, childArgs...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	go func() { _ = cmd.Wait() }()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		s, e := session.Read(id)
		if e == nil {
			return obj{"id": id, "panel": s.HTTP + "/#" + s.Token, "binary": abs, "project": root, "status": "paused at launch", "log": filepath.Join(dir, "delve.log")}, nil
		}
		if data, e := os.ReadFile(filepath.Join(dir, "error")); e == nil {
			return nil, fmt.Errorf("start failed: %s", data)
		}
	}
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("start timed out; inspect %s", filepath.Join(dir, "broker.log"))
}
