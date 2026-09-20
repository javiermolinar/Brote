package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Descriptor is the persisted session contract shared with the CLI and editors.
// Keep its JSON fields compatible with existing sessions and companion versions.
type Descriptor struct {
	BreakpointOwners map[string]string `json:"breakpointOwners,omitempty"`
	Backend          string            `json:"backend,omitempty"`
	Version          int               `json:"version,omitempty"`
	Binding          *Binding          `json:"binding,omitempty"`
	Events           []Event           `json:"events,omitempty"`
	Cursor           uint64            `json:"cursor,omitempty"`
	ID               string            `json:"id"`
	PID              int               `json:"brokerPid"`
	Binary           string            `json:"binary"`
	Project          string            `json:"project"`
	HTTP             string            `json:"http"`
	DAP              string            `json:"dap"`
	Token            string            `json:"token,omitempty"`
	Dir              string            `json:"directory"`
	Created          string            `json:"created"`
	RPC              string            `json:"rpc,omitempty"`
	DelvePID         int               `json:"delvePid,omitempty"`
	TargetPID        int               `json:"targetPid,omitempty"`
	Thread           string            `json:"thread,omitempty"`
	Codex            string            `json:"codex,omitempty"`
	Owner            string            `json:"owner,omitempty"`
	Editor           string            `json:"editor,omitempty"`
	HandoverID       string            `json:"handoverId,omitempty"`
	Watches          []string          `json:"watches,omitempty"`
	Fingerprint      *Fingerprint      `json:"fingerprint,omitempty"`
	Notification     *Notification     `json:"notification,omitempty"`
	Stopped          bool              `json:"stopped,omitempty"`
}

func NewID(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}

func Root() string {
	if p := os.Getenv("DEBUG_HANDOVER_HOME"); p != "" {
		return p
	}
	p, e := os.UserCacheDir()
	if e != nil {
		panic(e)
	}
	return filepath.Join(p, "debug-handover", "sessions")
}

func Read(id string) (Descriptor, error) {
	var s Descriptor
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return s, fmt.Errorf("invalid session ID")
	}
	b, e := os.ReadFile(filepath.Join(Root(), id, "session.json"))
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(b, &s)
	if e == nil && s.Version > 2 {
		return s, fmt.Errorf("session protocol %d is newer than supported version 2", s.Version)
	}
	return s, e
}

type Notification struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Created string `json:"created"`
}

// Binding identifies a client, not a harness conversation. It is not authentication.
type Binding struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Name     string `json:"name"`
}
type Event struct {
	ID      uint64   `json:"id"`
	Kind    string   `json:"kind"`
	Owner   string   `json:"owner"`
	Binding *Binding `json:"binding,omitempty"`
	Note    string   `json:"note,omitempty"`
	Created string   `json:"created"`
}
