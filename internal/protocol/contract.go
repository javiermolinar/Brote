// Package protocol defines the shared Brote service contract. Descriptor format
// and service API versions are separate so old session records remain readable.
package protocol

import "fmt"

const Version = 1

type Identity struct {
	Session    string `json:"session"`
	Run        string `json:"run"`
	Generation int    `json:"generation"`
	PauseEpoch int    `json:"pauseEpoch"`
}

type Request struct {
	Version int `json:"version"`
	Identity
	Client    string `json:"client"`
	CommandID string `json:"commandId"`
	Operation string `json:"operation"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Check validates the common envelope before an operation can reach Delve.
// A human pause may ignore an old generation, but never a different session/run.
func (r Request) Check(current Identity, supported map[string]bool, humanPause bool) error {
	if r.Version != Version {
		return &Error{"incompatible_protocol", fmt.Sprintf("service protocol %d is unsupported; expected %d", r.Version, Version)}
	}
	if r.Session == "" || r.Run == "" || r.Session != current.Session || r.Run != current.Run {
		return &Error{"identity_mismatch", "session or run changed; rediscover the session"}
	}
	if r.Client == "" || r.CommandID == "" {
		return &Error{"invalid_request", "client and commandId are required"}
	}
	if !supported[r.Operation] {
		return &Error{"unsupported_operation", "operation is not supported by this session: " + r.Operation}
	}
	if r.Generation != current.Generation && !(humanPause && r.Operation == "pause") {
		return &Error{"stale_revision", "session changed; refresh state before acting"}
	}
	return nil
}

// DAPCapabilities deliberately uses an allowlist. New Delve capabilities must
// not silently expose operations which bypass Brote's coordinator or handles.
func DAPCapabilities(adapter map[string]any) map[string]any {
	out := map[string]any{"supportsRestartRequest": true, "supportsTerminateRequest": true}
	for _, key := range []string{
		"supportsConfigurationDoneRequest", "supportsFunctionBreakpoints",
		"supportsConditionalBreakpoints", "supportsHitConditionalBreakpoints",
		"supportsEvaluateForHovers", "supportsExceptionInfoRequest",
		"supportsDelayedStackTraceLoading",
	} {
		if value, ok := adapter[key].(bool); ok && value {
			out[key] = true
		}
	}
	// These filters are implemented by Delve's setExceptionBreakpoints request.
	if value, ok := adapter["exceptionBreakpointFilters"]; ok {
		out["exceptionBreakpointFilters"] = value
	}
	return out
}

func DAPRequestSupported(command string) bool {
	switch command {
	case "initialize", "attach", "launch", "restart", "terminate", "configurationDone", "disconnect", "pause",
		"continue", "next", "stepIn", "stepOut", "threads", "stackTrace",
		"scopes", "variables", "evaluate", "exceptionInfo",
		"setBreakpoints", "setFunctionBreakpoints", "setExceptionBreakpoints":
		return true
	}
	return false
}

// Definition IDs belong to Brote, never to an adapter. Requested locations are
// persisted; resolved adapter locations are per-run observations.
type Scope struct {
	Workspace string `json:"workspace,omitempty"`
	Session   string `json:"session,omitempty"`
	Run       string `json:"run,omitempty"`
}
type Location struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Function string `json:"function,omitempty"`
}
type Definition struct {
	ID           string            `json:"id"`
	Revision     uint64            `json:"revision"`
	Scope        Scope             `json:"scope"`
	Kind         string            `json:"kind"`
	Owner        string            `json:"owner"`
	Location     Location          `json:"location"`
	Enabled      bool              `json:"enabled"`
	Condition    string            `json:"condition,omitempty"`
	HitCondition string            `json:"hitCondition,omitempty"`
	Name         string            `json:"name,omitempty"`
	Values       map[string]string `json:"values,omitempty"`
	CaptureLimit int               `json:"captureLimit,omitempty"`
}
type CaptureOutcome struct {
	Identity
	ID              string `json:"id"`
	DefinitionID    string `json:"definitionId"`
	Sequence        uint64 `json:"sequence"`
	Goroutine       int    `json:"goroutine"`
	Status          string `json:"status"`
	ExportStatus    string `json:"exportStatus"`
	ProgramTraceID  string `json:"programTraceId,omitempty"`
	DebuggerTraceID string `json:"debuggerTraceId,omitempty"`
	Error           *Error `json:"error,omitempty"`
}
type Event struct {
	Identity
	Cursor    uint64          `json:"cursor"`
	Kind      string          `json:"kind"`
	CommandID string          `json:"commandId,omitempty"`
	Capture   *CaptureOutcome `json:"capture,omitempty"`
}

// PathMapping maps editor source roots (From) to paths embedded in the target (To).
type PathMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}
