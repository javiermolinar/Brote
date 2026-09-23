package session

import (
	"agentdebugger/internal/definitions"
	"agentdebugger/internal/delivery"
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/telemetry"
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
	Consumers           map[string]Consumer `json:"consumers,omitempty"`
	TraceIDs            telemetry.IDs       `json:"traceIds,omitempty"`
	CaptureCounts       map[string]int      `json:"captureCounts,omitempty"`
	CaptureSequence     uint64              `json:"captureSequence,omitempty"`
	Definitions         definitions.Store   `json:"definitions,omitempty"`
	ExecutionCommands   []string            `json:"executionCommands,omitempty"`
	Attached            bool                `json:"attached,omitempty"`
	ServiceVersion      int                 `json:"serviceVersion,omitempty"`
	RunID               string              `json:"run,omitempty"`
	FunctionBreakpoints map[string]string   `json:"functionBreakpoints,omitempty"`
	Task                *ExecutionTask      `json:"task,omitempty"`
	BreakpointOwners    map[string]string   `json:"breakpointOwners,omitempty"`
	Backend             string              `json:"backend,omitempty"`
	Version             int                 `json:"version,omitempty"`
	Binding             *Binding            `json:"binding,omitempty"`
	Events              []Event             `json:"events,omitempty"`
	Cursor              uint64              `json:"cursor,omitempty"`
	ID                  string              `json:"id"`
	PID                 int                 `json:"brokerPid"`
	Binary              string              `json:"binary"`
	Project             string              `json:"project"`
	HTTP                string              `json:"http"`
	DAP                 string              `json:"dap"`
	Token               string              `json:"token,omitempty"`
	Dir                 string              `json:"directory"`
	Created             string              `json:"created"`
	RPC                 string              `json:"rpc,omitempty"`
	DelvePID            int                 `json:"delvePid,omitempty"`
	TargetPID           int                 `json:"targetPid,omitempty"`
	Thread              string              `json:"thread,omitempty"`
	Codex               string              `json:"codex,omitempty"`
	Owner               string              `json:"owner,omitempty"`
	Editor              string              `json:"editor,omitempty"`
	HandoverID          string              `json:"handoverId,omitempty"`
	Watches             []string            `json:"watches,omitempty"`
	Fingerprint         *Fingerprint        `json:"fingerprint,omitempty"`
	Notification        *Notification       `json:"notification,omitempty"`
	Stopped             bool                `json:"stopped,omitempty"`
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
	if e == nil && s.ID != id {
		return s, fmt.Errorf("session descriptor identity mismatch")
	}
	if e == nil && (s.ServiceVersion < 0 || s.ServiceVersion > 1) {
		return s, fmt.Errorf("unsupported service protocol %d", s.ServiceVersion)
	}
	if e == nil && s.ServiceVersion > 0 && (s.RunID == "" || s.Token == "") {
		return s, fmt.Errorf("incomplete service descriptor")
	}
	return s, e
}

type Notification struct {
	Attempt *delivery.Attempt `json:"attempt,omitempty"`
	Note    string            `json:"note,omitempty"`
	Run     string            `json:"run,omitempty"`
	ID      string            `json:"id"`
	Kind    string            `json:"kind"`
	Status  string            `json:"status"`
	Error   string            `json:"error,omitempty"`
	Created string            `json:"created"`
}

// Binding identifies a client, not a harness conversation. It is not authentication.
type Binding struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Name     string `json:"name"`
}
type Event struct {
	RunID      string   `json:"run,omitempty"`
	Generation int      `json:"generation,omitempty"`
	PauseEpoch int      `json:"pauseEpoch,omitempty"`
	ID         uint64   `json:"id"`
	Kind       string   `json:"kind"`
	Owner      string   `json:"owner"`
	Binding    *Binding `json:"binding,omitempty"`
	Note       string   `json:"note,omitempty"`
	Created    string   `json:"created"`
}

// ExecutionTask is a cooperative authorization grant, not an authentication token.
type ExecutionTask struct {
	HostConsumer  string            `json:"hostConsumer,omitempty"`
	HostInstance  string            `json:"hostInstance,omitempty"`
	HostTurn      string            `json:"hostTurn,omitempty"`
	Attempt       *delivery.Attempt `json:"attempt,omitempty"`
	Run           string            `json:"run,omitempty"`
	Delivery      string            `json:"delivery,omitempty"`
	DeliveryError string            `json:"deliveryError,omitempty"`
	ID            string            `json:"id"`
	Instruction   string            `json:"instruction"`
	Status        string            `json:"status"`
	Reason        string            `json:"reason,omitempty"`
	Binding       *Binding          `json:"binding"`
	Expires       string            `json:"expires"`
}

// Consumer progress is service-owned; it never constitutes execution scope.
type Consumer struct {
	Host             *HostState         `json:"host,omitempty"`
	Challenge        string             `json:"challenge,omitempty"`
	ChallengeExpires string             `json:"challengeExpires,omitempty"`
	ID               string             `json:"id"`
	Instance         string             `json:"instance"`
	Recipient        protocol.Recipient `json:"recipient"`
	Cursor           uint64             `json:"cursor"`
}

type HostState struct {
	protocol.HostFact
	Expires string `json:"expires,omitempty"`
}
