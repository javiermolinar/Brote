package cli

import (
	"agentdebugger/internal/session"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSetupRepairAndRemoval(t *testing.T) {
	temp := t.TempDir()
	root := filepath.Join(temp, "install")
	bundle := filepath.Join(temp, "bundle")
	stub := filepath.Join(temp, "stubs")
	t.Setenv("DELVE_LLM_ADAPTER_HOME", root)
	t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(temp, "sessions"))
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, d := range []string{stub, filepath.Join(bundle, "bin"), filepath.Join(bundle, "adapters", "pi"), filepath.Join(bundle, "editors")} {
		if e := os.MkdirAll(d, 0755); e != nil {
			t.Fatal(e)
		}
	}
	log := filepath.Join(temp, "calls")
	t.Setenv("INSTALL_TEST_LOG", log)
	for _, name := range []string{"dlv", "codex", "pi", "code"} {
		if e := os.WriteFile(filepath.Join(stub, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$INSTALL_TEST_LOG\"\n"), 0755); e != nil {
			t.Fatal(e)
		}
	}
	os.WriteFile(filepath.Join(bundle, "bin", "delve-llm-adapter"), []byte("binary fixture"), 0755)
	if e := session.Write(filepath.Join(bundle, "release.json"), obj{"version": "v0.2.0-test", "os": runtime.GOOS, "arch": runtime.GOARCH}); e != nil {
		t.Fatal(e)
	}
	// A failed host registration persists partial state and can be repaired.
	if err := os.WriteFile(filepath.Join(stub, "pi"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := installation("setup", []string{"--bundle", bundle, "--agent", "pi"}); err == nil {
		t.Fatal("expected host registration failure")
	}
	installed, err := os.ReadFile(filepath.Join(root, "installation.json"))
	if err != nil || !strings.Contains(string(installed), "failed:") {
		t.Fatal("partial failure was not recorded")
	}
	if err := os.WriteFile(filepath.Join(stub, "pi"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := installation("repair", nil); err != nil {
		t.Fatal(err)
	}
	// Installing both surfaces, then repeating setup, preserves prior components.
	if _, e := installation("setup", []string{"--bundle", bundle, "--agent", "pi", "--editor", "vscode"}); e != nil {
		t.Fatal(e)
	}
	if _, e := installation("setup", []string{"--bundle", bundle, "--agent", "codex"}); e != nil {
		t.Fatal(e)
	}
	if _, e := installation("repair", nil); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"brote", "agentdebugger", "delve-llm-adapter", "debug-handover"} {
		if _, e := os.Stat(filepath.Join(root, "bin", name)); e != nil {
			t.Fatal(name, e)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "marketplace", ".agents", "plugins", "marketplace.json")); err != nil {
		t.Fatal("missing Codex marketplace discovery path", err)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "plugin add brote@brote") || !strings.Contains(string(data), "--install-extension") {
		t.Fatal(string(data))
	}
	if _, e := installation("uninstall", []string{"--component", "core"}); e == nil {
		t.Fatal("removed core with integrations")
	}
	for _, name := range []string{"codex", "pi", "vscode"} {
		if _, e := installation("uninstall", []string{"--component", name}); e != nil {
			t.Fatal(e)
		}
	}
	activeDir := filepath.Join(session.Root(), "0123456789")
	if err := os.MkdirAll(activeDir, 0700); err != nil {
		t.Fatal(err)
	}
	active := session.Descriptor{ID: "0123456789", PID: os.Getpid()}
	if err := session.Write(filepath.Join(activeDir, "session.json"), active); err != nil {
		t.Fatal(err)
	}
	if _, err := installation("uninstall", []string{"--component", "core"}); err == nil {
		t.Fatal("removed core during active session")
	}
	active.Stopped = true
	if err := session.Write(filepath.Join(activeDir, "session.json"), active); err != nil {
		t.Fatal(err)
	}
	if _, e := installation("uninstall", []string{"--component", "core"}); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Lstat(filepath.Join(root, "bin", "debug-handover")); !os.IsNotExist(e) {
		t.Fatal("launcher retained")
	}
}
func TestManagedLinkPreservesUnrelatedFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "existing")
	os.WriteFile(file, []byte("user data"), 0600)
	if e := managedLink(filepath.Join(dir, "target"), file, dir); e == nil {
		t.Fatal("overwrote unrelated file")
	}
	data, _ := os.ReadFile(file)
	if string(data) != "user data" {
		t.Fatal(string(data))
	}
}

func TestExecutableBundleFollowsLauncherAndCurrentSymlinks(t *testing.T) {
	root := t.TempDir()
	release := filepath.Join(root, "releases", "v-test")
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(release, "bin", "delve-llm-adapter")
	if err := os.WriteFile(executable, []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(release, current); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "launcher")
	if err := os.Symlink(filepath.Join(current, "bin", "delve-llm-adapter"), launcher); err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(release)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{launcher, executable} {
		got, err := executableBundle(path)
		if err != nil || got != expected {
			t.Fatalf("bundle(%s) = %s, %v", path, got, err)
		}
	}
}

func TestUpgradeMigratesRecordedLegacyIntegrations(t *testing.T) {
	for _, tt := range []struct {
		name, plugin, market, extension string
		state                           installationState
	}{
		{"legacy", "debug-handover@delve-llm-adapter", "delve-llm-adapter", "debug-handover-local.debug-handover", installationState{Version: "v0.3.1"}},
		{"agentdebugger", "agentdebugger@agentdebugger", "agentdebugger", "agentdebugger-local.agentdebugger", installationState{Version: "0.4.0", CodexPlugin: "agentdebugger@agentdebugger", CodexMarketplace: "agentdebugger", VSCodeExtension: "agentdebugger-local.agentdebugger"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "install")
			bundle := filepath.Join(dir, "bundle")
			stub := filepath.Join(dir, "stub")
			t.Setenv("DELVE_LLM_ADAPTER_HOME", root)
			t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(dir, "sessions"))
			t.Setenv("PATH", stub)
			for _, d := range []string{root, stub, filepath.Join(bundle, "bin")} {
				if err := os.MkdirAll(d, 0755); err != nil {
					t.Fatal(err)
				}
			}
			log := filepath.Join(dir, "calls")
			t.Setenv("INSTALL_TEST_LOG", log)
			for _, name := range []string{"codex", "code"} {
				if err := os.WriteFile(filepath.Join(stub, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$INSTALL_TEST_LOG\"\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(bundle, "bin", "delve-llm-adapter"), []byte("fixture"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := session.Write(filepath.Join(bundle, "release.json"), obj{"version": "0.4.0", "os": runtime.GOOS, "arch": runtime.GOARCH, "VSCodeExtension": "release-owner.brote"}); err != nil {
				t.Fatal(err)
			}
			old := tt.state
			old.Components = map[string]string{"core": "installed", "codex": "installed", "vscode": "installed"}
			if err := session.Write(filepath.Join(root, "installation.json"), old); err != nil {
				t.Fatal(err)
			}
			// No Go, Delve or Node executable on PATH: installation itself needs none.
			if _, err := installation("setup", []string{"--bundle", bundle}); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(log)
			calls := string(data)
			for _, call := range []string{"plugin remove " + tt.plugin, "plugin marketplace remove " + tt.market, "plugin add brote@brote", "--uninstall-extension " + tt.extension} {
				if !strings.Contains(calls, call) {
					t.Fatalf("missing %q in %s", call, calls)
				}
			}
			if _, err := installation("repair", nil); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(log)
			if strings.Count(string(data), "plugin remove "+tt.plugin) != 1 {
				t.Fatal("repair repeated legacy migration")
			}
			if _, err := installation("uninstall", []string{"--component", "vscode"}); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(log)
			if !strings.Contains(string(data), "--uninstall-extension release-owner.brote") {
				t.Fatal("did not remove actual installed identity")
			}
		})
	}
}

func TestCanonicalSetupSelectionAndRepair(t *testing.T) {
	temp := t.TempDir()
	root := filepath.Join(temp, "install")
	bundle := filepath.Join(temp, "bundle")
	stubs := filepath.Join(temp, "stubs")
	t.Setenv("DELVE_LLM_ADAPTER_HOME", root)
	t.Setenv("DEBUG_HANDOVER_HOME", filepath.Join(temp, "sessions"))
	t.Setenv("PATH", stubs+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := Run([]string{"system", "setup"}); err == nil || !strings.Contains(err.Error(), "choose components") {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("fresh setup mutated installation", err)
	}
	for _, dir := range []string{stubs, filepath.Join(bundle, "bin"), filepath.Join(bundle, "adapters", "pi"), filepath.Join(bundle, "editors")} {
		os.MkdirAll(dir, 0755)
	}
	log := filepath.Join(temp, "calls")
	t.Setenv("INSTALL_TEST_LOG", log)
	for _, name := range []string{"pi", "code"} {
		os.WriteFile(filepath.Join(stubs, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$INSTALL_TEST_LOG\"\n"), 0755)
	}
	os.WriteFile(filepath.Join(bundle, "bin", "delve-llm-adapter"), []byte("fixture"), 0755)
	session.Write(filepath.Join(bundle, "release.json"), obj{"version": "v-test", "os": runtime.GOOS, "arch": runtime.GOARCH})
	// Partial installation records the requested integration for a no-option repair.
	os.WriteFile(filepath.Join(stubs, "pi"), []byte("#!/bin/sh\nexit 1\n"), 0755)
	if _, err := Run([]string{"system", "setup", "--bundle", bundle, "--agent", "pi", "--editor", "vscode"}); err == nil {
		t.Fatal("expected partial failure")
	}
	os.WriteFile(filepath.Join(stubs, "pi"), []byte("#!/bin/sh\nprintf 'pi\\n' >> \"$INSTALL_TEST_LOG\"\n"), 0755)
	var partial installationState
	data, err := os.ReadFile(filepath.Join(root, "installation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &partial); err != nil || partial.Components["vscode"] != "pending" {
		t.Fatal("lost unattempted selection", partial, err)
	}
	if _, err := Run([]string{"system", "setup"}); err != nil {
		t.Fatal(err)
	}
	repairedCalls, _ := os.ReadFile(log)
	if !strings.Contains(string(repairedCalls), "pi") || !strings.Contains(string(repairedCalls), "--install-extension") {
		t.Fatal("repair omitted original selection", string(repairedCalls))
	}
	os.WriteFile(log, nil, 0600)
	if _, err := Run([]string{"system", "setup", "--editor", "vscode"}); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "pi") {
		t.Fatal("explicit setup reinstalled unrequested integration", string(calls))
	}
	os.WriteFile(log, nil, 0600)
	if _, err := Run([]string{"system", "setup"}); err != nil {
		t.Fatal(err)
	}
	calls, _ = os.ReadFile(log)
	if !strings.Contains(string(calls), "pi") || !strings.Contains(string(calls), "--install-extension") {
		t.Fatal("repair lost installed choices", string(calls))
	}
	before, _ := os.ReadFile(filepath.Join(root, "installation.json"))
	if _, err := Run([]string{"system", "status"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "installation.json"))
	if string(before) != string(after) {
		t.Fatal("status changed installation")
	}
}
