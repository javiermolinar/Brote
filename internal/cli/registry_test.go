package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryResolution(t *testing.T) {
	for _, path := range []string{"session start", "session attach", "session list", "session open", "session stop", "session detach", "session restart", "session history", "session configs", "session recover", "session cleanup", "session bind", "session events watch", "session events wait", "session events acknowledge", "session events retry", "debug state", "debug capabilities", "debug sources", "debug goroutines", "debug stack", "debug eval", "debug watch add", "debug watch remove", "debug continue", "debug pause", "debug step", "debug breakpoint list", "debug tracepoint update", "debug captures", "debug task start", "debug task execute", "debug task heartbeat", "debug task complete", "debug task cancel", "query traces", "comment status", "system setup", "system status", "system uninstall", "system doctor", "system version", "break", "clear", "handover", "reclaim", "workspace", "saved-run", "dap", "serve", "trace-serve", "trace-service", "ui-serve", "bridge"} {
		t.Run(path, func(t *testing.T) {
			c, _, rest, _, err := resolveCommand(commandRegistry(), append(strings.Fields(path), "ID"))
			if err != nil || c.run == nil || len(rest) != 1 || rest[0] != "ID" {
				t.Fatalf("resolution: %v %v", rest, err)
			}
		})
	}
}
func TestRegistryRejectsUnknownBeforeSessionRead(t *testing.T) {
	for _, args := range [][]string{{"nonsense", "missing"}, {"debug", "nonsense", "missing"}, {"debug", "task", "nonsense", "missing"}, {"debug"}} {
		_, err := Run(args)
		if err == nil || !(strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "subcommand required")) {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
func TestRegistryHelpAndProgramArguments(t *testing.T) {
	var out bytes.Buffer
	if _, err := runCLI([]string{"debug", "state", "missing", "--help"}, &out); err != nil || !strings.Contains(out.String(), "brote debug state") {
		t.Fatal(out.String(), err)
	}
	args := []string{"session", "start", "--binary", "target", "--", "--help", "debug"}
	_, _, rest, help, err := resolveCommand(commandRegistry(), args)
	if err != nil || help || strings.Join(rest, " ") != "--binary target -- --help debug" {
		t.Fatalf("%v %v %v", rest, help, err)
	}
}

func TestEveryPublicCommandHasOfflineHelp(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "absent")
	t.Setenv("DEBUG_HANDOVER_HOME", rootDir)
	t.Setenv("AGENTDEBUGGER_DATA_DIR", rootDir)
	var walk func(*command, []string)
	walk = func(c *command, path []string) {
		if c.hidden {
			return
		}
		for _, args := range [][]string{append(append([]string{}, path...), "--help"), append([]string{"help"}, path...)} {
			var out, fail bytes.Buffer
			if code := Execute(args, &out, &fail); code != 0 || fail.Len() != 0 {
				t.Fatalf("%v: %d %s", args, code, fail.String())
			}
			if c.run != nil && (!strings.Contains(out.String(), "Usage:") || !strings.Contains(out.String(), "Example:")) {
				t.Fatalf("incomplete help for %v", path)
			}
		}
		for _, child := range c.children {
			walk(child, append(append([]string{}, path...), child.name))
		}
	}
	walk(commandRegistry(), nil)
	if _, err := os.Stat(rootDir); !os.IsNotExist(err) {
		t.Fatal("help touched session/storage directories", err)
	}
	var out bytes.Buffer
	runCLI(nil, &out)
	for _, hidden := range []string{"trace-serve", "task-start", "embedded-tempo", "--binary"} {
		if strings.Contains(out.String(), hidden) {
			t.Fatal("root exposes manual/internal commands", out.String())
		}
	}
}
func TestRunnerFlagErrorsUseInjectedStderr(t *testing.T) {
	var out, fail bytes.Buffer
	if code := Execute([]string{"session", "start", "--invalid"}, &out, &fail); code != 1 || out.Len() != 0 || !json.Valid(fail.Bytes()) {
		t.Fatal(code, out.String(), fail.String())
	}
}
