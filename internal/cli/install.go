package cli

import (
	"context"
	"crypto/sha256"
	"agentdebugger/internal/session"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var Version = "dev"

type installationState struct {
	Version    string            `json:"version"`
	Components map[string]string `json:"components"`
	Bundle     string            `json:"bundle"`
}

func installRoot() string {
	if p := os.Getenv("DELVE_LLM_ADAPTER_HOME"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "delve-llm-adapter")
}
func installBinRoot(root string) string {
	if os.Getenv("DELVE_LLM_ADAPTER_HOME") != "" {
		return filepath.Join(root, "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle must not contain symlinks: %s", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular bundle file: %s", p)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
func managedLink(target, link, root string) error {
	if old, err := os.Readlink(link); err == nil {
		if !strings.HasPrefix(old, root+string(os.PathSeparator)) {
			return fmt.Errorf("refusing to replace unmanaged link %s", link)
		}
	} else if _, err := os.Lstat(link); err == nil {
		return fmt.Errorf("refusing to replace existing file %s", link)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		return err
	}
	tmp := link + ".new-" + session.NewID(4)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, link)
}
func hostCommand(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}
func installation(verb string, args []string) (any, error) {
	f := flag.NewFlagSet(verb, flag.ContinueOnError)
	agent := f.String("agent", "", "codex or pi")
	editor := f.String("editor", "", "optional vscode")
	bundle := f.String("bundle", "", "extracted release directory")
	component := f.String("component", "", "component to remove: codex, pi, vscode, core")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	if *agent != "" && *agent != "codex" && *agent != "pi" {
		return nil, fmt.Errorf("agent must be codex or pi")
	}
	if *editor != "" && *editor != "vscode" {
		return nil, fmt.Errorf("editor must be vscode")
	}
	root, err := filepath.Abs(installRoot())
	if err != nil {
		return nil, err
	}
	statePath := filepath.Join(root, "installation.json")
	state := installationState{Components: map[string]string{}}
	if data, err := os.ReadFile(statePath); err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return nil, err
		}
	}
	if state.Components == nil {
		state.Components = map[string]string{}
	}
	if verb == "installation" {
		return obj{"root": root, "installation": state, "platform": runtime.GOOS + "/" + runtime.GOARCH}, nil
	}
	if err = os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	lock, err := session.Lock(root)
	if err != nil {
		return nil, err
	}
	defer session.Unlock(lock)
	// Reload after acquiring the lock, in case another setup just completed.
	if data, err := os.ReadFile(statePath); err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return nil, err
		}
	}
	save := func() error { return session.Write(statePath, state) }
	run := func(key string, fn func() error) error {
		if err := fn(); err != nil {
			state.Components[key] = "failed: " + err.Error()
			_ = save()
			return err
		}
		state.Components[key] = "installed"
		return save()
	}
	if verb == "uninstall" {
		if *component == "" {
			return nil, fmt.Errorf("choose --component codex|pi|vscode|core")
		}
		var err error
		switch *component {
		case "codex":
			err = hostCommand("codex", "plugin", "remove", "debug-handover@delve-llm-adapter", "--json")
			if err == nil {
				err = hostCommand("codex", "plugin", "marketplace", "remove", "delve-llm-adapter", "--json")
			}
		case "pi":
			err = hostCommand("pi", "remove", filepath.Join(root, "current", "adapters", "pi"))
		case "vscode":
			err = hostCommand("code", "--uninstall-extension", "debug-handover-local.debug-handover")
		case "core":
			for key, value := range state.Components {
				if key != "core" && value != "removed" {
					return nil, fmt.Errorf("remove %s integration first", key)
				}
			}
			entries, _ := os.ReadDir(session.Root())
			for _, entry := range entries {
				s, e := session.Read(entry.Name())
				if e == nil && !s.Stopped && (session.ProcessExists(s.PID) || session.ProcessExists(s.DelvePID) || session.ProcessExists(s.TargetPID)) {
					return nil, fmt.Errorf("session %s is active; core retained", s.ID)
				}
			}
			for _, name := range []string{"agentdebugger", "delve-llm-adapter", "debug-handover"} {
				link := filepath.Join(installBinRoot(root), name)
				if dest, e := os.Readlink(link); e == nil && strings.HasPrefix(dest, root+"/") {
					_ = os.Remove(link)
				}
			}
			err = os.RemoveAll(filepath.Join(root, "releases"))
			if err == nil {
				_ = os.Remove(filepath.Join(root, "current"))
			}
		default:
			return nil, fmt.Errorf("unknown component")
		}
		if err != nil {
			return nil, err
		}
		state.Components[*component] = "removed"
		return state, save()
	}
	if verb == "repair" {
		if *bundle == "" {
			*bundle = state.Bundle
		}
	}
	if *bundle == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		*bundle, err = executableBundle(exe)
		if err != nil {
			return nil, err
		}
	}
	source, err := filepath.EvalSymlinks(*bundle)
	if err != nil {
		return nil, err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(source, "release.json"))
	if err != nil {
		return nil, fmt.Errorf("use an extracted release bundle (--bundle): %w", err)
	}
	var manifest struct{ Version, OS, Arch string }
	if err = json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`).MatchString(manifest.Version) || manifest.OS != runtime.GOOS || manifest.Arch != runtime.GOARCH {
		return nil, fmt.Errorf("incompatible release manifest")
	}
	if _, err := exec.LookPath("dlv"); err != nil {
		home, _ := os.UserHomeDir()
		if _, err = os.Stat(filepath.Join(home, "go", "bin", "dlv")); err != nil {
			return nil, fmt.Errorf("Delve required: install dlv and rerun setup")
		}
	}
	release := filepath.Join(root, "releases", manifest.Version)
	if source != release {
		if _, err := os.Stat(release); err == nil {
			a, e1 := os.ReadFile(filepath.Join(source, "bin", "delve-llm-adapter"))
			b, e2 := os.ReadFile(filepath.Join(release, "bin", "delve-llm-adapter"))
			if e1 != nil || e2 != nil || sha256.Sum256(a) != sha256.Sum256(b) {
				return nil, fmt.Errorf("version %s already installed with different binary; use a new release version", manifest.Version)
			}
		}
		if _, err := os.Stat(release); os.IsNotExist(err) {
			tmp := release + ".tmp-" + session.NewID(4)
			defer os.RemoveAll(tmp)
			if err = copyTree(source, tmp); err != nil {
				return nil, err
			}
			if err = os.Rename(tmp, release); err != nil {
				return nil, err
			}
		}
	}
	if err = managedLink(release, filepath.Join(root, "current"), root); err != nil {
		return nil, err
	}
	binDir := installBinRoot(root)
	for _, name := range []string{"agentdebugger", "delve-llm-adapter", "debug-handover"} {
		if err = managedLink(filepath.Join(root, "current", "bin", "delve-llm-adapter"), filepath.Join(binDir, name), root); err != nil {
			return nil, err
		}
	}
	state.Version = manifest.Version
	state.Bundle = release
	state.Components["core"] = "installed"
	if err = save(); err != nil {
		return nil, err
	}
	installCodex := func() error {
		marketplace := filepath.Join(root, "marketplace")
		plugin := filepath.Join(marketplace, "plugins", "debug-handover")
		if err := os.MkdirAll(filepath.Dir(plugin), 0755); err != nil {
			return err
		}
		if err := managedLink(filepath.Join(root, "current", "adapters", "codex", "debug-handover"), plugin, root); err != nil {
			return err
		}
		market := obj{"name": "delve-llm-adapter", "interface": obj{"displayName": "Delve LLM Adapter"}, "plugins": []any{obj{"name": "debug-handover", "source": obj{"source": "local", "path": "./plugins/debug-handover"}, "policy": obj{"installation": "AVAILABLE", "authentication": "ON_INSTALL"}, "category": "Productivity"}}}
		catalogDir := filepath.Join(marketplace, ".agents", "plugins")
		if err := os.MkdirAll(catalogDir, 0755); err != nil {
			return err
		}
		if err := session.Write(filepath.Join(catalogDir, "marketplace.json"), market); err != nil {
			return err
		}
		if err := hostCommand("codex", "plugin", "marketplace", "add", marketplace, "--json"); err != nil {
			return err
		}
		return hostCommand("codex", "plugin", "add", "debug-handover@delve-llm-adapter", "--json")
	}
	installPi := func() error { return hostCommand("pi", "install", filepath.Join(root, "current", "adapters", "pi")) }
	installEditor := func() error {
		return hostCommand("code", "--install-extension", filepath.Join(root, "current", "editors", "debug-handover.vsix"), "--force")
	}
	for _, item := range []struct {
		name     string
		selected bool
		fn       func() error
	}{{"codex", *agent == "codex", installCodex}, {"pi", *agent == "pi", installPi}, {"vscode", *editor == "vscode", installEditor}} {
		if item.selected || (state.Components[item.name] != "" && state.Components[item.name] != "removed") {
			if err = run(item.name, item.fn); err != nil {
				return nil, err
			}
		}
	}
	return obj{"installation": state, "bin": binDir, "message": "Browser inspector included. Add the bin directory to PATH if needed; restart/reload the selected harness."}, nil
}

// Resolve the launcher before taking its parent: ~/.local/bin is a symlink
// surface, not the release bundle that contains release.json.
func executableBundle(exe string) (string, error) {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(resolved)), nil
}
