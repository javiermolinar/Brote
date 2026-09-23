package protocol

import "fmt"

// CoordinationVersion is additive to service v1. Consumers must negotiate it;
// an older broker is never treated as implementing managed delivery.
const CoordinationVersion = 1

// Recipient is routing identity, not authentication or execution authorization.
// A provider recipient can answer questions but cannot claim an execution task.
type Recipient struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Name     string `json:"name,omitempty"`
}

func (r Recipient) Check() error {
	if (r.Kind != "agent" && r.Kind != "provider") || r.ID == "" || len(r.ID) > 256 || r.Revision == 0 || len(r.Name) > 80 {
		return &Error{"invalid_request", "recipient kind, ID and positive revision required"}
	}
	return nil
}

// DeliveryEnvelope identifies an external-send attempt. Run fences execution
// subjects; evidenceRun on a question is provenance, not reply authority.
type DeliveryEnvelope struct {
	Version     int       `json:"version"`
	Session     string    `json:"session"`
	Run         string    `json:"run,omitempty"`
	Kind        string    `json:"kind"`
	Subject     string    `json:"subject"`
	Thread      string    `json:"thread,omitempty"`
	Attempt     string    `json:"attempt"`
	Recipient   Recipient `json:"recipient"`
	Cursor      uint64    `json:"cursor"`
	Status      string    `json:"status"`
	Message     string    `json:"message"`
	EvidenceID  string    `json:"evidenceId,omitempty"`
	EvidenceRun string    `json:"evidenceRun,omitempty"`
}

func (e DeliveryEnvelope) Check() error {
	if e.Version != CoordinationVersion {
		return &Error{"incompatible_protocol", "unsupported coordination version"}
	}
	if err := e.Recipient.Check(); err != nil {
		return err
	}
	if e.Session == "" || e.Subject == "" || e.Attempt == "" {
		return &Error{"invalid_request", "session, subject and attempt required"}
	}
	switch e.Kind {
	case "handback", "task":
		if e.Run == "" || e.Recipient.Kind != "agent" {
			return &Error{"invalid_request", "execution subjects require a run and agent recipient"}
		}
	case "question":
		if e.Thread == "" {
			return &Error{"invalid_request", "question thread required"}
		}
	default:
		return &Error{"invalid_request", fmt.Sprintf("unknown delivery kind %q", e.Kind)}
	}
	return nil
}

// HostFact reports observable host state. Sequence and service-issued instance
// fence old processes/turns. Only a response to a fresh challenge proves liveness.
type HostFact struct {
	Instance  string `json:"instance"`
	Sequence  uint64 `json:"sequence"`
	Turn      string `json:"turn,omitempty"`
	State     string `json:"state"`
	Challenge string `json:"challenge,omitempty"`
}

func (f HostFact) Check() error {
	if f.Instance == "" || f.Sequence == 0 || len(f.Turn) > 128 {
		return &Error{"invalid_request", "host instance and positive sequence required"}
	}
	if f.State != "active" && f.State != "idle" && f.State != "closed" {
		return &Error{"invalid_request", "host state must be active, idle or closed"}
	}
	if f.State == "active" && f.Turn == "" {
		return &Error{"invalid_request", "active host fact requires a turn"}
	}
	return nil
}
