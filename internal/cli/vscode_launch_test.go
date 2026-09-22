package cli

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentdebugger/internal/session"
)

func TestSourceConfigRequiresExplicitBuild(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "launch.json")
	if err := os.WriteFile(path, []byte(`{"configurations":[{"name":"Go","type":"go","request":"launch","program":"."}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--launch-file", path}, "--build"},
		{[]string{"--launch-file", path, "--binary", "/unused"}, "cannot be combined"},
		{[]string{"--binary", "/unused", "--build"}, "require --config"},
	} {
		args := append([]string{"--thread", "", "--project", root}, tc.args...)
		if _, err := start(args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestIntegrationVSCodeLaunchAndRunAgain(t *testing.T) {
	if os.Getenv("DH_INTEGRATION") != "1" {
		t.Skip("set DH_INTEGRATION=1 to test real Delve")
	}
	root := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(root, "sessions"))
	t.Setenv("AGENTDEBUGGER_DATA_DIR", filepath.Join(root, "history"))
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("BROTE_CONFIG_UNSET", "remove-me")
	project := filepath.Join(root, "project with spaces")
	cwd := filepath.Join(project, "working directory")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(project, "go.mod"), "module example.com/config\n\ngo 1.23\n")
	write(filepath.Join(project, "main.go"), `package main
import ("os"; "encoding/json")
func main() {
 cwd, _ := os.Getwd()
 data, _ := json.Marshal(map[string]any{"cwd":cwd,"args":os.Args[1:],"value":os.Getenv("BROTE_CONFIG_VALUE"),"file":os.Getenv("BROTE_CONFIG_FILE"),"unset":os.Getenv("BROTE_CONFIG_UNSET"),"isolation":os.Getenv("DEBUG_HANDOVER_HOME")})
 if err := os.WriteFile("result.json",data,0600); err != nil { panic(err) }
}
`)
	write(filepath.Join(project, "test.env"), "BROTE_CONFIG_VALUE=file-value\nBROTE_CONFIG_FILE=from-env-file\n")
	launchPath := filepath.Join(project, "launch.json")
	write(launchPath, `{// comment
 "configurations":[{"name":"Server","type":"go","request":"launch","mode":"debug","program":"${workspaceFolder}","cwd":"${workspaceFolder}/working directory","args":["two words"],"envFile":"test.env","env":{"BROTE_CONFIG_VALUE":"config-value","BROTE_CONFIG_UNSET":null,"DEBUG_HANDOVER_HOME":"target-only"},}],
}`)
	helper := filepath.Join(root, "brote")
	if out, err := exec.Command("go", "build", "-race", "-o", helper, "../../cmd/brote").CombinedOutput(); err != nil {
		t.Fatalf("build helper: %s: %v", out, err)
	}
	run := func(args ...string) obj {
		t.Helper()
		out, err := exec.Command(helper, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s: %v", args, out, err)
		}
		var result obj
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("%v: %s: %v", args, out, err)
		}
		return result
	}
	var sessions []string
	t.Cleanup(func() {
		for _, id := range sessions {
			if s, err := session.Read(id); err == nil {
				_ = syscall.Kill(s.PID, syscall.SIGTERM)
				_ = syscall.Kill(s.DelvePID, syscall.SIGTERM)
			}
		}
		time.Sleep(200 * time.Millisecond)
	})
	first := run("start", "--config", "Server", "--launch-file", launchPath, "--project", project, "--build", "--no-ui", "--", "extra")
	firstID := str(first["id"])
	sessions = append(sessions, firstID)
	check := func(id string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(cwd, "result.json")); !os.IsNotExist(err) {
			t.Fatal("target ran before continue")
		}
		state := run("state", id, "--summary")
		if str(state["status"]) != "paused" {
			t.Fatalf("not paused at launch: %v", state)
		}
		run("continue", id, "--human", "--wait", "10s")
		var data []byte
		var err error
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			data, err = os.ReadFile(filepath.Join(cwd, "result.json"))
			if err == nil {
				break
			}
		}
		var actual obj
		if err != nil || json.Unmarshal(data, &actual) != nil {
			t.Fatalf("target result: %s, %v", data, err)
		}
		want := obj{"cwd": cwd, "args": []any{"two words", "extra"}, "value": "config-value", "file": "from-env-file", "unset": "", "isolation": "target-only"}
		// Compare physical paths as macOS may retain the /var alias via PWD.
		want["cwd"], _ = filepath.EvalSymlinks(cwd)
		actual["cwd"], _ = filepath.EvalSymlinks(str(actual["cwd"]))
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("got %v, want %v", actual, want)
		}
		run("end-session", id, "--confirmed")
		if err := os.Remove(filepath.Join(cwd, "result.json")); err != nil {
			t.Fatal(err)
		}
	}
	check(firstID)
	binary := str(first["binary"])
	before, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := session.FindHistory(firstID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := session.ReadLaunchSettings(archive)
	if err != nil || settings == nil || settings.Cwd != cwd {
		t.Fatalf("saved settings: %+v, %v", settings, err)
	}
	st, err := os.Stat(filepath.Join(archive, "launch.json"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("launch settings must be private: %v, %v", st, err)
	}
	for _, file := range []string{"session.json", "events.jsonl"} {
		data, err := os.ReadFile(filepath.Join(archive, file))
		if err != nil || strings.Contains(string(data), "config-value") {
			t.Fatalf("public history contains environment: %s: %v", file, err)
		}
	}
	// A saved run uses the built executable and resolved settings, even if the
	// original config, environment file and source change or disappear.
	write(filepath.Join(project, "main.go"), "invalid source")
	if err := os.Remove(launchPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(project, "test.env")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "history", "workspace"), "disable test UI")
	second := run("run-again", firstID)
	secondID := str(second["id"])
	sessions = append(sessions, secondID)
	check(secondID)
	after, err := os.ReadFile(binary)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("run-again rebuilt the target", err)
	}
}

func TestIntegrationVSCodeTestProfiles(t *testing.T) {
	if os.Getenv("DH_INTEGRATION") != "1" {
		t.Skip("set DH_INTEGRATION=1 to test real Delve")
	}
	root := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(root, "sessions"))
	t.Setenv("AGENTDEBUGGER_DATA_DIR", filepath.Join(root, "history"))
	t.Setenv("CODEX_THREAD_ID", "")
	project := filepath.Join(root, "library")
	if err := os.MkdirAll(filepath.Join(project, ".vscode"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(project, "go.mod"), "module example.com/library\n\ngo 1.23\n")
	write(filepath.Join(project, "library.go"), "package library\nfunc Add(a, b int) int { return a + b }\n")
	// An external test package needs the whole package, including library.go and
	// the sibling test helper. Compiling only the selected file cannot work.
	write(filepath.Join(project, "helper_test.go"), "package library_test\nconst expected = 42\n")
	source := `package library_test
import (
 "os"
 "testing"
 library "example.com/library"
)
func TestSelected(t *testing.T) {
 t.Run("chosen", func(t *testing.T) {
  got := library.Add(19, 23)
  if got != expected { t.Fatalf("got %d", got) } // BREAK_HERE
  if os.Getenv("BROTE_TEST_PROFILE") != "enabled" { t.Fatal("missing environment") }
  if err := os.WriteFile("selected.result", []byte("passed"), 0600); err != nil { t.Fatal(err) }
 })
 t.Run("other", func(t *testing.T) {
  _ = os.WriteFile("unselected.result", []byte("unexpected"), 0600)
  t.Fatal("unselected subtest ran")
 })
}
func TestUnselected(t *testing.T) {
 _ = os.WriteFile("unselected.result", []byte("unexpected"), 0600)
 t.Fatal("unselected test ran")
}
`
	file := filepath.Join(project, "library_test.go")
	write(file, source)
	breakLine := 1 + strings.Count(source[:strings.Index(source, "  if got != expected")], "\n")
	write(filepath.Join(project, ".vscode", "launch.json"), `{
 "configurations": [
  {"name":"Package tests","type":"go","request":"launch","mode":"test","program":"${workspaceFolder}","args":["-test.run","^TestSelected$/^chosen$"],"env":{"BROTE_TEST_PROFILE":"enabled"}},
  {"name":"Test file","type":"go","request":"launch","mode":"test","program":"${file}","args":["-test.run","^TestSelected$/^chosen$"],"env":{"BROTE_TEST_PROFILE":"enabled"}},
  {"name":"Auto tests","type":"go","request":"launch","mode":"auto","program":"${fileDirname}","args":["-test.run","^TestSelected$/^chosen$"],"env":{"BROTE_TEST_PROFILE":"enabled"}}
 ]
}`)
	helper := filepath.Join(root, "brote")
	if out, err := exec.Command("go", "build", "-race", "-o", helper, "../../cmd/brote").CombinedOutput(); err != nil {
		t.Fatalf("build helper: %s: %v", out, err)
	}
	for _, name := range []string{"Package tests", "Test file", "Auto tests"} {
		t.Run(name, func(t *testing.T) {
			run := func(args ...string) obj {
				t.Helper()
				out, err := exec.Command(helper, args...).CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %s: %v", args, out, err)
				}
				var result obj
				if err := json.Unmarshal(out, &result); err != nil {
					t.Fatalf("%v: %s: %v", args, out, err)
				}
				return result
			}
			launched := run("start", "--project", project, "--config", name, "--file", file, "--build", "--no-ui", "--", "-test.count=1")
			id := str(launched["id"])
			t.Cleanup(func() {
				if s, err := session.Read(id); err == nil {
					_ = syscall.Kill(s.PID, syscall.SIGTERM)
					_ = syscall.Kill(s.DelvePID, syscall.SIGTERM)
				}
				time.Sleep(200 * time.Millisecond)
			})
			state := run("state", id, "--summary")
			if str(state["status"]) != "paused" {
				t.Fatalf("test binary did not start paused: %v", state)
			}
			assertNotRun := func(name string) {
				t.Helper()
				if _, err := os.Stat(filepath.Join(project, name)); !os.IsNotExist(err) {
					t.Fatalf("test ran unexpectedly: %s: %v", name, err)
				}
			}
			assertNotRun("selected.result")
			assertNotRun("unselected.result")
			run("break", id, "--file", file, "--line", strconv.Itoa(breakLine))
			binding := asObj(state["binding"])
			request := run("task-start", id, "--binding", str(binding["id"]), "--revision", strconv.Itoa(num(binding["revision"])), "--instruction", "Debug the selected test to its breakpoint, inspect got, then let it finish")
			task := asObj(request["task"])
			if str(task["delivery"]) != "acknowledged" {
				t.Fatal("the current conversation's request was queued for duplicate delivery")
			}
			execute := func() obj {
				return run("task-execute", id, "--binding", str(binding["id"]), "--task", str(task["id"]), "--operation", "continue", "--wait", "10s")
			}
			paused := execute()
			frames := asList(paused["frames"])
			if str(paused["status"]) != "paused" || len(frames) == 0 || num(asObj(frames[0])["line"]) != breakLine {
				t.Fatalf("test breakpoint was not hit: %v", paused)
			}
			evaluated := run("eval", id, "--expression", "got")
			if str(asObj(evaluated["value"])["value"]) != "42" {
				t.Fatalf("test local is unavailable: %v", evaluated)
			}
			exited := execute()
			if str(exited["status"]) != "exited" || num(asObj(exited["state"])["exitStatus"]) != 0 {
				t.Fatalf("test binary failed: %v", exited)
			}
			data, err := os.ReadFile(filepath.Join(project, "selected.result"))
			if err != nil || string(data) != "passed" {
				t.Fatalf("selected test result: %s, %v", data, err)
			}
			assertNotRun("unselected.result")
			completed := run("task-complete", id, "--binding", str(binding["id"]), "--task", str(task["id"]))
			if str(asObj(completed["task"])["status"]) != "completed" {
				t.Fatal("finished test retained an execution task")
			}
			run("end-session", id, "--confirmed")
			if err := os.Remove(filepath.Join(project, "selected.result")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
