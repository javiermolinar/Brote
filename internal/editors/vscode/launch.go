// Package vscode reads the portable subset of Go launch.json configurations.
package vscode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"agentdebugger/internal/jsonc"
	"agentdebugger/internal/session"
)

type Profile struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Request string `json:"request"`
	Mode    string `json:"mode,omitempty"`
}

type Launch struct {
	Profile
	Program    string
	Args       []string
	BuildFlags []string
	Delve      string
	Settings   session.LaunchSettings
}

type launchFile struct {
	Configurations []map[string]json.RawMessage `json:"configurations"`
}

func read(project, path string) ([]map[string]json.RawMessage, error) {
	if path == "" {
		path = filepath.Join(project, ".vscode", "launch.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data, err = jsonc.StripComments(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var file launchFile
	if err = json.Unmarshal(jsonc.StripTrailingCommas(data), &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file.Configurations, nil
}

func platformConfig(raw map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	config := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		config[key] = value
	}
	platform := map[string]string{"darwin": "osx", "linux": "linux", "windows": "windows"}[runtime.GOOS]
	if value, ok := config[platform]; ok {
		var overrides map[string]json.RawMessage
		if err := json.Unmarshal(value, &overrides); err != nil {
			return nil, fmt.Errorf("%s overrides: %w", platform, err)
		}
		for key, value := range overrides {
			config[key] = value
		}
	}
	for _, key := range []string{"osx", "linux", "windows"} {
		delete(config, key)
	}
	return config, nil
}

func profile(raw map[string]json.RawMessage) (Profile, error) {
	data, err := json.Marshal(raw)
	var p Profile
	if err == nil {
		err = json.Unmarshal(data, &p)
	}
	return p, err
}

// List returns names and debugger types without resolving variables or reading
// environment files. Listing a workspace must not expose its credentials.
func List(project, path string) ([]Profile, error) {
	configs, err := read(project, path)
	if err != nil {
		return nil, err
	}
	result := []Profile{}
	for _, raw := range configs {
		config, err := platformConfig(raw)
		if err != nil {
			return nil, err
		}
		p, err := profile(config)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, nil
}

func Load(project, path, name, file string) (*Launch, error) {
	root, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	configs, err := read(root, path)
	if err != nil {
		return nil, err
	}
	var selected map[string]json.RawMessage
	names := []string{}
	for _, raw := range configs {
		config, err := platformConfig(raw)
		if err != nil {
			return nil, err
		}
		p, err := profile(config)
		if err != nil {
			return nil, err
		}
		if name != "" && p.Name == name || name == "" && p.Type == "go" && p.Request == "launch" {
			names = append(names, p.Name)
			selected = config
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("launch configuration %q not found; use 'brote session configs --project %s' to list profiles", name, root)
	}
	if len(names) != 1 {
		return nil, fmt.Errorf("launch configuration is ambiguous (%s); select a unique name with --config", strings.Join(names, ", "))
	}
	launch, err := resolve(selected, root, file)
	if err != nil {
		return nil, fmt.Errorf("launch configuration %q: %w", names[0], err)
	}
	return launch, nil
}

func resolve(config map[string]json.RawMessage, root, file string) (*Launch, error) {
	p, err := profile(config)
	if err != nil {
		return nil, err
	}
	if p.Type != "go" || p.Request != "launch" {
		return nil, fmt.Errorf("only type 'go' with request 'launch' is supported (got %q / %q)", p.Type, p.Request)
	}
	// Reject unsupported semantics rather than silently launching a different
	// program, skipping tasks or pretending to provide an interactive terminal.
	allowed := strings.Fields("name type request mode program args cwd env envFile buildFlags dlvToolPath stopOnEntry console debugAdapter presentation internalConsoleOptions substitutePath")
	known := map[string]bool{}
	for _, key := range allowed {
		known[key] = true
	}
	unsupported := []string{}
	for key := range config {
		if !known[key] {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return nil, fmt.Errorf("unsupported launch.json attributes: %s", strings.Join(unsupported, ", "))
	}
	expand := expander{root: root, file: file}
	if file != "" {
		expand.file = absolute(root, file)
	}
	getString := func(key string) (string, error) {
		var value string
		if raw, ok := config[key]; ok {
			if err := json.Unmarshal(raw, &value); err != nil {
				return "", fmt.Errorf("%s must be a string", key)
			}
		}
		value, err := expand.string(value)
		if err != nil {
			return "", fmt.Errorf("%s: %w", key, err)
		}
		return value, nil
	}
	for key, supported := range map[string]string{"console": "internalConsole", "debugAdapter": "dlv-dap"} {
		value, err := getString(key)
		if err != nil {
			return nil, err
		}
		if value != "" && value != supported {
			return nil, fmt.Errorf("%s=%q is unsupported; only %q is supported", key, value, supported)
		}
	}
	program, err := getString("program")
	if err != nil {
		return nil, err
	}
	if program == "" {
		return nil, fmt.Errorf("program is required")
	}
	program = absolute(root, program)
	if p.Mode == "" {
		p.Mode = "debug"
	}
	if p.Mode == "auto" {
		active := expand.file
		if strings.HasSuffix(program, ".go") {
			active = program
		}
		p.Mode = "debug"
		if strings.HasSuffix(active, "_test.go") {
			p.Mode = "test"
		}
	}
	if p.Mode != "exec" && p.Mode != "debug" && p.Mode != "test" {
		return nil, fmt.Errorf("mode %q is unsupported; use exec, debug, test or auto", p.Mode)
	}
	if p.Mode == "test" && strings.HasSuffix(program, "_test.go") {
		program = filepath.Dir(program)
	}
	st, err := os.Stat(program)
	if err != nil {
		return nil, fmt.Errorf("program: %w", err)
	}
	if p.Mode == "exec" && (st.IsDir() || st.Mode()&0111 == 0) {
		return nil, fmt.Errorf("program is not executable: %s", program)
	}
	if p.Mode != "exec" && !st.IsDir() && !strings.HasSuffix(program, ".go") {
		return nil, fmt.Errorf("program must be a directory or .go file for mode %s", p.Mode)
	}
	cwd, err := getString("cwd")
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		cwd = program
		if !st.IsDir() {
			cwd = filepath.Dir(program)
		}
	}
	cwd = absolute(root, cwd)
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("cwd is not a directory: %s", cwd)
	}
	l := &Launch{Profile: p, Program: program, Settings: session.LaunchSettings{Cwd: cwd, Env: map[string]*string{}}}
	if raw, ok := config["substitutePath"]; ok {
		if err := json.Unmarshal(raw, &l.Settings.SubstitutePath); err != nil {
			return nil, fmt.Errorf("substitutePath must be an array of from/to mappings: %w", err)
		}
		if len(l.Settings.SubstitutePath) > 32 {
			return nil, fmt.Errorf("substitutePath permits at most 32 mappings")
		}
		for i := range l.Settings.SubstitutePath {
			m := &l.Settings.SubstitutePath[i]
			m.From, err = expand.string(m.From)
			if err != nil {
				return nil, err
			}
			m.To, err = expand.string(m.To)
			if err != nil {
				return nil, err
			}
			if !filepath.IsAbs(m.From) || m.To == "" {
				return nil, fmt.Errorf("substitutePath requires an absolute local from path and nonempty target to path")
			}
		}
	}
	for key, dest := range map[string]*[]string{"args": &l.Args, "buildFlags": &l.BuildFlags} {
		if raw, ok := config[key]; ok {
			values, err := stringList(raw, true, expand)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			*dest = values
		}
	}
	if p.Mode == "exec" && len(l.BuildFlags) > 0 {
		return nil, fmt.Errorf("buildFlags do not apply to mode exec")
	}
	if err := validateBuildFlags(l.BuildFlags); err != nil {
		return nil, err
	}
	l.Delve, err = getString("dlvToolPath")
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(l.Delve, `/\`) {
		l.Delve = absolute(root, l.Delve)
	}
	if raw, ok := config["envFile"]; ok {
		files, err := stringList(raw, false, expand)
		if err != nil {
			return nil, fmt.Errorf("envFile: %w", err)
		}
		for _, file := range files {
			if file == "" {
				continue
			}
			values, err := readEnvFile(absolute(root, file))
			if err != nil {
				return nil, err
			}
			for key, value := range values {
				l.Settings.Env[key] = value
			}
		}
	}
	if raw, ok := config["env"]; ok {
		var values map[string]*string
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("env must map names to strings or null: %w", err)
		}
		for key, value := range values {
			if key == "" || strings.ContainsAny(key, "=\x00") {
				return nil, fmt.Errorf("invalid environment variable name")
			}
			if value != nil {
				expanded, err := expand.string(*value)
				if err != nil {
					return nil, fmt.Errorf("env %s: %w", key, err)
				}
				value = &expanded
			}
			l.Settings.Env[key] = value
		}
	}
	return l, nil
}

func absolute(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func stringList(raw json.RawMessage, split bool, expand expander) ([]string, error) {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		value, err := expand.string(single)
		if err != nil {
			return nil, err
		}
		if split {
			return splitArgs(value)
		}
		return []string{value}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("expected a string or array of strings")
	}
	for i, value := range values {
		expanded, err := expand.string(value)
		if err != nil {
			return nil, err
		}
		values[i] = expanded
	}
	return values, nil
}
