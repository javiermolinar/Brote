package cli

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"debug-handover/internal/agents/codex"
	"debug-handover/internal/session"
)

func doctor(args []string) (obj, error) {
	f := flag.NewFlagSet("doctor", flag.ContinueOnError)
	binary := f.String("binary", "", "optional target")
	project := f.String("project", ".", "project")
	if e := f.Parse(args); e != nil {
		return nil, e
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
	checks["automaticHandover"] = obj{"available": queueErr == nil, "error": errorString(queueErr)}
	result := obj{"checks": checks, "platforms": "macOS and Linux; Go/Delve compatibility depends on the target build"}
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
