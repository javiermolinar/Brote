// Package delivery owns external-message delivery policy, independent of hosts
// and Delve. Persist a claim before sending; an ambiguous send is never retried.
package delivery

import (
	"agentdebugger/internal/protocol"
	"fmt"
)

type Attempt struct {
	ID        string             `json:"id"`
	Consumer  string             `json:"consumer"`
	Instance  string             `json:"instance"`
	Recipient protocol.Recipient `json:"recipient"`
	Run       string             `json:"run,omitempty"`
}

func (a *Attempt) Check(id, consumer, instance string, r protocol.Recipient) error {
	if a == nil || id == "" || id != a.ID || consumer != a.Consumer || instance != a.Instance || r.Kind != a.Recipient.Kind || r.ID != a.Recipient.ID || r.Revision != a.Recipient.Revision {
		return fmt.Errorf("delivery attempt or recipient is obsolete")
	}
	return nil
}

// Transition keeps recipient acknowledgements ahead of late sender receipts.
// The returned state is authoritative even when it differs from requested.
func Transition(current, next string) (string, error) {
	if current == next && next != "sending" {
		return current, nil
	}
	if current == "answered" || current == "thinking" || current == "acknowledged" {
		switch next {
		case "queued", "failed", "unknown", "thinking", "acknowledged":
			return current, nil
		}
	}
	switch next {
	case "sending":
		if current == "pending" {
			return next, nil
		}
	case "queued", "failed", "unknown":
		if current == "sending" {
			return next, nil
		}
	case "acknowledged", "thinking":
		if current == "sending" || current == "queued" || current == "unknown" {
			return next, nil
		}
	case "answered":
		if current == "sending" || current == "queued" || current == "unknown" || current == "thinking" || current == "acknowledged" {
			return next, nil
		}
	}
	return current, fmt.Errorf("invalid delivery transition %s -> %s", current, next)
}

// Interrupted returns uncertainty rather than permission to reinject.
func Interrupted(status string) string {
	if status == "sending" {
		return "unknown"
	}
	return status
}
