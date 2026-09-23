package session

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/telemetry"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LaunchSettings is kept in a private file, not the public session metadata or
// event stream: environment overrides may contain credentials.
type LaunchSettings struct {
	Service        bool                   `json:"service,omitempty"`
	OTLP           *telemetry.Config      `json:"otlp,omitempty"`
	OTLPError      string                 `json:"otlpError,omitempty"`
	Args           []string               `json:"args,omitempty"`
	Delve          string                 `json:"delve,omitempty"`
	SubstitutePath []protocol.PathMapping `json:"substitutePath,omitempty"`
	Cwd            string                 `json:"cwd,omitempty"`
	Env            map[string]*string     `json:"env,omitempty"`
}

func ReadLaunchSettings(dir string) (*LaunchSettings, error) {
	data, err := os.ReadFile(filepath.Join(dir, "launch.json"))
	if os.IsNotExist(err) {
		return nil, nil // Sessions created before launch settings were supported.
	}
	if err != nil {
		return nil, err
	}
	var settings LaunchSettings
	err = json.Unmarshal(data, &settings)
	return &settings, err
}

func (s *LaunchSettings) Environment(base []string) []string {
	if s == nil || len(s.Env) == 0 {
		return base
	}
	values := map[string]string{}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range s.Env {
		if value == nil {
			delete(values, key)
		} else {
			values[key] = *value
		}
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	sort.Strings(result)
	return result
}
