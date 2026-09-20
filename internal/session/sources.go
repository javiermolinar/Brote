package session

import (
	"agentdebugger/internal/delve"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"time"
)

// Sources exposes only files recorded in the running binary's debug information.
func Sources(s Descriptor, file string) (map[string]any, error) {
	host, _, err := net.SplitHostPort(s.RPC)
	if err != nil || host != "127.0.0.1" {
		return nil, fmt.Errorf("invalid Delve endpoint")
	}
	state, err := delve.Call(s.RPC, "State", map[string]any{"NonBlocking": true}, time.Second*3)
	if err != nil {
		return nil, err
	}
	debugState, _ := state["State"].(map[string]any)
	if debugState["Pid"] != float64(s.TargetPID) {
		return nil, fmt.Errorf("target identity changed")
	}
	result, err := delve.Call(s.RPC, "ListSources", map[string]any{"Filter": ""}, time.Second*3)
	if err != nil {
		return nil, err
	}
	files := []string{}
	allowed := false
	if items, ok := result["Sources"].([]any); ok {
		for _, raw := range items {
			if path, ok := raw.(string); ok {
				files = append(files, path)
				if path == file {
					allowed = true
				}
			}
		}
	}
	sort.Strings(files)
	if file == "" {
		return map[string]any{"files": files}, nil
	}
	if !allowed {
		return nil, fmt.Errorf("file is not present in this binary's debug information")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return nil, fmt.Errorf("source unavailable or larger than 2 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 {
		return nil, fmt.Errorf("source too large")
	}
	return map[string]any{"file": file, "start": 1, "line": 0, "lines": strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")}, nil
}
