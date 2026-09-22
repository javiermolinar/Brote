package vscode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func fixture(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".vscode"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".vscode", "launch.json"), config)
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/launch\n\ngo 1.23\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadJSONCVariablesEnvironmentAndPlatform(t *testing.T) {
	t.Setenv("BROTE_LAUNCH_TEST", "outer value")
	platform := map[string]string{"darwin": "osx", "linux": "linux", "windows": "windows"}[runtime.GOOS]
	root := fixture(t, `{
 // Other adapters and unresolved variables must not prevent reading this file.
 "configurations": [
  {"name":"Node", "type":"node", "request":"launch", "program":"${command:node}"},
  {"name":"Server", "type":"go", "request":"launch", "mode":"debug",
   "program":"${workspaceFolder}/main.go", "cwd":"${workspaceFolder}",
   "args":["wrong"], "envFile":["first.env", "second.env"],
   "env":{"OVERRIDE":"${env:BROTE_LAUNCH_TEST}","DROP":null,"ROOT":"${workspaceFolderBasename}"},
   "buildFlags":"-tags 'dev integration'",
   "`+platform+`":{"args":["http://localhost/*literal*/", "${fileBasename}", "${env:BROTE_LAUNCH_TEST}", "literal,}",]},
  },
 ],
}`)
	writeFile(t, filepath.Join(root, "first.env"), "BASE=first\nOVERRIDE=first\nCHAIN=${BASE}/path\nESCAPED=\\${BASE}\n")
	writeFile(t, filepath.Join(root, "second.env"), "\ufeffexport BASE=second\nOVERRIDE=file\nMULTI=\"a\\nb\"\nQUOTED=\"'literal'\"\n# ignored\n")
	profiles, err := List(root, "")
	if err != nil || len(profiles) != 2 || profiles[1].Name != "Server" {
		t.Fatalf("profiles = %+v, %v", profiles, err)
	}
	l, err := Load(root, "", "Server", "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if l.Program != filepath.Join(root, "main.go") || l.Settings.Cwd != root {
		t.Fatalf("wrong paths: %+v", l)
	}
	if !reflect.DeepEqual(l.Args, []string{"http://localhost/*literal*/", "main.go", "outer value", "literal,}"}) {
		t.Fatal(l.Args)
	}
	if !reflect.DeepEqual(l.BuildFlags, []string{"-tags", "dev integration"}) {
		t.Fatal(l.BuildFlags)
	}
	for key, want := range map[string]string{"BASE": "second", "OVERRIDE": "outer value", "CHAIN": "first/path", "ESCAPED": "${BASE}", "MULTI": "a\nb", "QUOTED": "'literal'", "ROOT": filepath.Base(root)} {
		if got := l.Settings.Env[key]; got == nil || *got != want {
			t.Errorf("env %s = %v, want %q", key, got, want)
		}
	}
	if got := l.Settings.Environment([]string{"DROP=gone", "INHERITED=yes", "OVERRIDE=old"}); strings.Contains(strings.Join(got, "\n"), "DROP=") || !strings.Contains(strings.Join(got, "\n"), "INHERITED=yes") {
		t.Fatal(got)
	}
	data, _ := json.Marshal(profiles)
	if strings.Contains(string(data), "OVERRIDE") {
		t.Fatalf("listing exposed environment: %s", data)
	}
}

func TestSelectionAndUnsupportedSemantics(t *testing.T) {
	for _, tc := range []struct{ name, attributes, want string }{
		{"task", `,"preLaunchTask":"build"`, "preLaunchTask"},
		{"command", `,"args":["${command:pickProcess}"]`, "unsupported VS Code variable"},
		{"input", `,"args":["${input:port}"]`, "unsupported VS Code variable"},
		{"file", `,"args":["${file}"]`, "--file"},
		{"foreign folder", `,"cwd":"${workspaceFolder:other}"`, "unsupported VS Code variable"},
		{"attach", `,"request":"attach"`, "request 'launch'"},
		{"remote", `,"mode":"remote"`, "mode"},
		{"terminal", `,"console":"integratedTerminal"`, "console"},
		{"mapping", `,"substitutePath":[]`, "substitutePath"},
		{"bad args", `,"args":[42]`, "array of strings"},
		{"bad env", `,"env":{"VALUE":42}`, "env must map"},
		{"NUL", `,"env":{"VALUE":"\u0000"}`, "NUL"},
		{"cwd", `,"cwd":"missing"`, "cwd is not a directory"},
		{"envFile", `,"envFile":"missing"`, "envFile"},
		{"output flag", `,"buildFlags":"-o /tmp/elsewhere"`, "cannot override"},
		{"debug flag", `,"buildFlags":"-gcflags=all=-N"`, "cannot override"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, `{"configurations":[{"name":"Go","type":"go","request":"launch","program":"."`+tc.attributes+`}]}`)
			if _, err := Load(root, "", "Go", ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	root := fixture(t, `{"configurations":[{"name":"One","type":"go","request":"launch","program":"."},{"name":"Two","type":"go","request":"launch","program":"."}]}`)
	if _, err := Load(root, "", "", ""); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal(err)
	}
	if _, err := Load(root, "", "Missing", ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatal(err)
	}
	if _, err := Load(root, "", "Two", ""); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".vscode", "launch.json"), `{"configurations":[/* unterminated`)
	if _, err := List(root, ""); err == nil {
		t.Fatal("accepted malformed JSONC")
	}
}

func TestAutoModeAndDefaultWorkingDirectory(t *testing.T) {
	root := fixture(t, `{"configurations":[{"name":"Go","type":"go","request":"launch","mode":"auto","program":"${file}"}]}`)
	writeFile(t, filepath.Join(root, "main_test.go"), "package main\n")
	l, err := Load(root, "", "", "main_test.go")
	if err != nil || l.Mode != "test" || l.Program != root || l.Settings.Cwd != root {
		t.Fatalf("%+v, %v", l, err)
	}
	l, err = Load(root, "", "Go", "main.go")
	if err != nil || l.Mode != "debug" || l.Settings.Cwd != root {
		t.Fatalf("%+v, %v", l, err)
	}
}

func TestQuotedArguments(t *testing.T) {
	got, err := splitArgs(`--name "two words" '' '$(literal)' back\ slash "\d+"`)
	want := []string{"--name", "two words", "", "$(literal)", "back slash", `\d+`}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%q, %v", got, err)
	}
	if _, err := splitArgs(`"unclosed`); err == nil {
		t.Fatal("accepted unclosed quote")
	}
	if err := validateBuildFlags([]string{"-tags", "c"}); err != nil {
		t.Fatalf("rejected a flag value that resembles a reserved option: %v", err)
	}
}

func TestExecProfileAndCustomLaunchFile(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := fixture(t, `{"configurations":[]}`)
	config := map[string]any{"configurations": []any{map[string]any{
		"name": "Existing", "type": "go", "request": "launch", "mode": "exec", "program": exe,
		"args": `--name "two words" ''`,
	}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "custom.json")
	writeFile(t, path, string(data))
	l, err := Load(root, path, "Existing", "")
	if err != nil {
		t.Fatal(err)
	}
	if l.Program != exe || l.Settings.Cwd != filepath.Dir(exe) || !reflect.DeepEqual(l.Args, []string{"--name", "two words", ""}) {
		t.Fatalf("wrong exec configuration: %+v", l)
	}
}

func TestBuildDebugAndTestProfiles(t *testing.T) {
	root := fixture(t, `{"configurations":[{"name":"Go","type":"go","request":"launch","mode":"auto","program":"${file}","buildFlags":["-tags=launchtest"],"env":{"BROTE_BUILD_TEST":"yes"}}]}`)
	writeFile(t, filepath.Join(root, "main.go"), "//go:build launchtest\n\npackage main\nimport \"fmt\"\nfunc main(){fmt.Print(\"debug-built\")}\n")
	writeFile(t, filepath.Join(root, "main_test.go"), "//go:build launchtest\n\npackage main\nimport \"testing\"\nfunc TestBuilt(t *testing.T) {}\n")
	for _, tc := range []struct{ file, want string }{{"main.go", "debug-built"}, {"main_test.go", "PASS"}} {
		t.Run(tc.file, func(t *testing.T) {
			l, err := Load(root, "", "Go", tc.file)
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "debug binary")
			if err := l.Build(output); err != nil {
				t.Fatal(err)
			}
			data, err := exec.Command(output).CombinedOutput()
			if err != nil || !strings.Contains(string(data), tc.want) {
				t.Fatalf("%s, %v", data, err)
			}
		})
	}
}
