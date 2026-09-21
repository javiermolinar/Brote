package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"agentdebugger/internal/agents/codex"
	"agentdebugger/internal/session"
)

func start(args []string) (result obj, err error) {
	f := flag.NewFlagSet("start", flag.ContinueOnError)
	noUI := f.Bool("no-ui", false, "return the broker URL without starting the persistent workspace")
	investigation := f.String("investigation", "", "existing investigation ID")
	title := f.String("title", "", "new investigation title")
	backend := f.String("backend", "dap", "debug backend: dap or legacy rpc")
	bin := f.String("binary", "", "Existing Go executable or test binary")
	project := f.String("project", ".", "Project/source directory")
	dlv := f.String("dlv", "dlv", "Delve executable")
	binding := f.String("binding", "", "opaque client binding ID")
	name := f.String("name", "Agent", "agent display name")
	thread := f.String("thread", os.Getenv("CODEX_THREAD_ID"), "Codex task for inspector handovers; empty disables wakeups")
	if e := f.Parse(args); e != nil {
		return nil, e
	}
	if *backend != "dap" && *backend != "rpc" {
		return nil, fmt.Errorf("backend must be dap or rpc")
	}
	if *bin == "" {
		return nil, fmt.Errorf("--binary is required; build once with go build -gcflags='all=-N -l'")
	}
	if *thread != "" && !codex.ValidThread(*thread) {
		return nil, fmt.Errorf("--thread must be a Codex task UUID")
	}
	if *binding == "" {
		*binding = session.NewID(16)
	}
	if *thread != "" {
		*name = "Codex"
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
		return nil, fmt.Errorf("Delve not found: install dlv for your Go version or pass --dlv /absolute/path/dlv; run agentdebugger doctor for diagnostics: %w", e)
	}
	id := session.NewID(5)
	if *investigation != "" {
		if _, err := session.ReadInvestigation(*investigation); err != nil {
			old, readErr := session.Read(*investigation)
			if readErr != nil {
				return nil, fmt.Errorf("investigation not found")
			}
			if err = session.AssignInvestigation(old.ID, old.ID, filepath.Base(old.Binary), old.Project); err != nil {
				return nil, err
			}
		}
	}
	if e = session.AssignInvestigation(id, *investigation, *title, root); e != nil {
		return nil, e
	}
	childStarted := false
	defer func() {
		if err != nil && !childStarted {
			d := session.Descriptor{ID: id, Project: root, Binary: abs, Created: time.Now().UTC().Format(time.RFC3339Nano)}
			if recordErr := session.RecordFailedLaunch(d, f.Args(), err.Error()); recordErr != nil {
				err = fmt.Errorf("%w; recording failed launch: %v", err, recordErr)
			}
		}
	}()
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
	childArgs := []string{"serve", "--backend", *backend, "--id", id, "--binary", abs, "--project", root, "--dlv", delve, "--binding", *binding, "--name", *name, "--"}
	childArgs = append(childArgs, f.Args()...)
	cmd := exec.Command(exe, childArgs...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	childStarted = true
	go func() { _ = cmd.Wait() }()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		s, e := session.Read(id)
		if e == nil {
			if *thread != "" {
				if err := configureBridge(s, *thread); err != nil {
					return obj{"id": id, "panel": s.HTTP, "notificationError": err.Error()}, nil
				}
			}
			panel := s.HTTP + "/#" + s.Token
			if !*noUI {
				if ui, err := ensureUI(); err == nil {
					panel = ui + "/?session=" + id
				}
			}
			return obj{"id": id, "panel": panel, "binary": abs, "project": root, "status": "paused at launch", "log": filepath.Join(dir, "delve.log")}, nil
		}
		if data, e := os.ReadFile(filepath.Join(dir, "error")); e == nil {
			return nil, fmt.Errorf("start failed: %s", data)
		}
	}
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("start timed out; inspect %s", filepath.Join(dir, "broker.log"))
}
