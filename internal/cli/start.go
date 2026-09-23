package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"agentdebugger/internal/agents/codex"
	"agentdebugger/internal/editors/vscode"
	"agentdebugger/internal/session"
)

func start(args []string) (obj, error) {
	return startWithLaunch(args, nil)
}

func startWithLaunch(args []string, settings *session.LaunchSettings) (result obj, err error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	f := flag.NewFlagSet("start", flag.ContinueOnError)
	editorStart := f.Bool("editor-start", false, "require editor configuration within 30 seconds")
	attachPID := f.Int("pid", 0, "attach to a local process using the supplied binary for source identity")
	service := f.Bool("service", settings == nil || settings.Service, "use the authenticated shared-service session contract (default for new launches)")
	legacy := f.Bool("legacy", false, "explicitly use the legacy session contract for direct Zed/RPC compatibility")
	noUI := f.Bool("no-ui", false, "return the broker URL without starting the persistent workspace")
	investigation := f.String("investigation", "", "existing investigation ID")
	title := f.String("title", "", "new investigation title")
	backend := f.String("backend", "dap", "debug backend: dap or legacy rpc")
	bin := f.String("binary", "", "Existing Go executable or test binary")
	config := f.String("config", "", "VS Code launch configuration name")
	launchFile := f.String("launch-file", "", "VS Code launch.json path (default: PROJECT/.vscode/launch.json)")
	activeFile := f.String("file", "", "Active source file for VS Code variables and auto mode")
	build := f.Bool("build", false, "Build the selected Go debug/test configuration before launching")
	project := f.String("project", ".", "Project/source directory")
	dlv := f.String("dlv", "dlv", "Delve executable")
	binding := f.String("binding", "", "opaque client binding ID")
	name := f.String("name", "Agent", "agent display name")
	thread := f.String("thread", os.Getenv("CODEX_THREAD_ID"), "Codex task for inspector handovers; empty disables wakeups")
	if e := f.Parse(args); e != nil {
		return nil, e
	}
	if *legacy {
		explicitService := false
		f.Visit(func(flag *flag.Flag) {
			if flag.Name == "service" {
				explicitService = true
			}
		})
		if explicitService && *service {
			return nil, fmt.Errorf("--legacy conflicts with --service=true")
		}
		*service = false
	}
	if *attachPID > 0 && !*service {
		return nil, fmt.Errorf("process attach requires shared service mode; omit --legacy/--service=false")
	}
	if *backend != "dap" && *backend != "rpc" {
		return nil, fmt.Errorf("backend must be dap or rpc")
	}
	fromConfig := *config != "" || *launchFile != ""
	if *editorStart && (!*service || *attachPID > 0) {
		return nil, fmt.Errorf("editor-start requires a new shared-service launch; process attach is unsupported")
	}
	if *attachPID < 0 {
		return nil, fmt.Errorf("--pid must be positive")
	}
	if *attachPID > 0 {
		if fromConfig || *build || len(f.Args()) > 0 {
			return nil, fmt.Errorf("process attach cannot build or pass program arguments")
		}
		if !session.ProcessExists(*attachPID) {
			return nil, fmt.Errorf("attach target does not exist")
		}
		*service = true
	}
	if *bin == "" && !fromConfig {
		return nil, fmt.Errorf("--binary or --config is required; use 'brote configs' to list VS Code profiles")
	}
	if fromConfig && *bin != "" {
		return nil, fmt.Errorf("--binary cannot be combined with --config or --launch-file")
	}
	if !fromConfig && (*build || *activeFile != "") {
		return nil, fmt.Errorf("--build and --file require --config or --launch-file")
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
	root, e := filepath.Abs(*project)
	if e != nil {
		return nil, e
	}
	if st, e := os.Stat(root); e != nil || !st.IsDir() {
		return nil, fmt.Errorf("project directory does not exist: %s", root)
	}
	programArgs := f.Args()
	var launch *vscode.Launch
	if fromConfig {
		launch, e = vscode.Load(root, *launchFile, *config, *activeFile)
		if e != nil {
			return nil, e
		}
		if launch.Mode != "exec" && !*build {
			return nil, fmt.Errorf("configuration %q uses mode %s and needs a build; pass --build, or use an exec configuration with a precompiled binary", launch.Name, launch.Mode)
		}
		if launch.Mode == "exec" && *build {
			return nil, fmt.Errorf("--build does not apply to an exec configuration")
		}
		settings = &launch.Settings
		if *title == "" {
			*title = launch.Name
		}
		programArgs = append(append([]string{}, launch.Args...), programArgs...)
		dlvExplicit := false
		f.Visit(func(flag *flag.Flag) {
			if flag.Name == "dlv" {
				dlvExplicit = true
			}
		})
		if !dlvExplicit && launch.Delve != "" {
			*dlv = launch.Delve
		}
		*bin = launch.Program
	}
	if *backend != "dap" && settings != nil && len(settings.SubstitutePath) > 0 {
		return nil, fmt.Errorf("substitutePath requires the DAP backend")
	}
	if *service && *backend != "dap" {
		return nil, fmt.Errorf("shared service sessions require the DAP backend; use --legacy --backend rpc for the old contract")
	}
	id := session.NewID(5)
	dir, e := filepath.Abs(filepath.Join(session.Root(), id))
	if e != nil {
		return nil, e
	}
	if launch != nil && launch.Mode != "exec" {
		*bin = filepath.Join(dir, "target")
	}
	abs, e := filepath.Abs(*bin)
	if e != nil {
		return nil, e
	}
	if launch == nil || launch.Mode == "exec" {
		st, e := os.Stat(abs)
		if e != nil {
			return nil, e
		}
		if st.IsDir() || st.Mode()&0111 == 0 {
			return nil, fmt.Errorf("binary is not executable: %s", abs)
		}
	}
	if settings != nil && settings.Cwd != "" {
		if st, e := os.Stat(settings.Cwd); e != nil || !st.IsDir() {
			return nil, fmt.Errorf("launch working directory does not exist: %s", settings.Cwd)
		}
	}
	delve, e := exec.LookPath(*dlv)
	if e != nil && *dlv == "dlv" {
		home, _ := os.UserHomeDir()
		delve, e = exec.LookPath(filepath.Join(home, "go", "bin", "dlv"))
	}
	if e != nil {
		return nil, fmt.Errorf("Delve not found: install dlv for your Go version or pass --dlv /absolute/path/dlv; run brote doctor for diagnostics: %w", e)
	}
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
			if recordErr := session.RecordFailedLaunch(d, programArgs, err.Error()); recordErr != nil {
				err = fmt.Errorf("%w; recording failed launch: %v", err, recordErr)
			}
		}
	}()
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if settings != nil {
		if e = session.Write(filepath.Join(dir, "launch.json"), settings); e != nil {
			return nil, e
		}
	}
	if launch != nil && launch.Mode != "exec" {
		if e = launch.BuildContext(ctx, abs); e != nil {
			return nil, e
		}
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
	if *attachPID > 0 {
		childArgs = append(childArgs[:len(childArgs)-1], "--pid", fmt.Sprint(*attachPID), "--")
	}
	if *service {
		childArgs = append(childArgs[:len(childArgs)-1], "--service", "--")
	}
	if *editorStart {
		if !*service {
			return nil, fmt.Errorf("editor-start requires service mode")
		}
		childArgs = append(childArgs[:len(childArgs)-1], "--editor-start", "--")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	childArgs = append(childArgs, programArgs...)
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
			if ctx.Err() != nil {
				_, _ = session.End(context.Background(), id)
				return nil, ctx.Err()
			}
			if *thread != "" {
				if err := configureBridge(s, *thread); err != nil {
					return obj{"id": id, "run": s.RunID, "serviceVersion": s.ServiceVersion, "panel": s.HTTP, "binary": abs, "project": root, "status": "paused at launch", "notificationError": err.Error()}, nil
				}
			}
			panel := s.HTTP
			if !*service {
				panel += "/#" + s.Token
			}
			if !*noUI && !*service {
				if ui, err := ensureUI(); err == nil {
					panel = ui + "/?session=" + id
				}
			}
			return obj{"id": id, "run": s.RunID, "serviceVersion": s.ServiceVersion, "panel": panel, "binary": abs, "project": root, "status": "paused at launch", "log": filepath.Join(dir, "delve.log")}, nil
		}
		if data, e := os.ReadFile(filepath.Join(dir, "error")); e == nil {
			return nil, fmt.Errorf("start failed: %s", data)
		}
	}
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("start timed out; inspect %s", filepath.Join(dir, "broker.log"))
}
