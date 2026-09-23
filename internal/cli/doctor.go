package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentdebugger/internal/agents/codex"
	"agentdebugger/internal/session"
)

func doctor(args []string) (obj, error)          { return doctorCommand(args, false) }
func canonicalDoctor(args []string) (obj, error) { return doctorCommand(args, true) }
func doctorCommand(args []string, strict bool) (obj, error) {
	f := newFlagSet("doctor")
	binary := f.String("binary", "", "optional target")
	project := f.String("project", ".", "project")
	if e := f.Parse(args); e != nil {
		return nil, e
	}
	if strict && f.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments")
	}
	checks := obj{}
	for name, argv := range map[string][]string{"go": {"version"}, "dlv": {"version"}, "zed": {"--version"}, "code": {"--version"}, "codex": {"--version"}, "pi": {"--version"}} {
		path, e := exec.LookPath(name)
		if e != nil && name == "dlv" {
			home, _ := os.UserHomeDir()
			path, e = exec.LookPath(filepath.Join(home, "go", "bin", "dlv"))
		}
		if e != nil {
			checks[name] = obj{"available": false, "error": e.Error()}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, path, argv...).CombinedOutput()
		cancel()
		checks[name] = obj{"available": err == nil, "path": path, "version": strings.TrimSpace(string(out)), "error": errorString(err)}
	}
	_, queueErr := codex.Find()
	checks["codexEventBridge"] = obj{"available": queueErr == nil, "error": errorString(queueErr), "requires": "codex queue --thread --message"}
	result := obj{"version": Version, "protocol": 2, "testedHosts": obj{"codex": "0.154.0", "pi": "0.85.1", "vscode": "1.138.0"}, "debuggerSetup": "Install a Delve build compatible with the target Go version, or start with --dlv /absolute/path/dlv. Go is needed to build targets, not to install Brote releases.", "checks": checks, "platforms": "macOS and Linux; Go/Delve compatibility depends on the target build"}
	if *binary != "" {
		abs, e := filepath.Abs(*binary)
		if e != nil {
			return nil, e
		}
		info, e := os.Stat(abs)
		if e != nil {
			return nil, e
		}
		result["executable"] = info.Mode()&0111 != 0
		result["debugInfo"] = session.HasDebugInfo(abs)
		root, _ := filepath.Abs(*project)
		result["fingerprint"] = session.CaptureFingerprint(abs, root)
	}
	return result, nil
}
