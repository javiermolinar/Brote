// Package traceinfo defines immutable discussion and annotation trace records.
// It has no dependency on a debugger, exporter, or live process.
package traceinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxBody = 16000

type Target struct {
	Session   string `json:"session"` // tracing service session identity (session:executionRun for shared sessions)
	TraceID   string `json:"traceId"`
	SpanID    string `json:"spanId,omitempty"`
	CaptureID string `json:"captureId,omitempty"`
}

type Record struct {
	ID            string    `json:"id"`
	Revision      uint64    `json:"revision"`
	Kind          string    `json:"kind"` // conversation or annotation
	Session       string    `json:"session"`
	TraceID       string    `json:"traceId"`
	ParentID      string    `json:"parentId"`
	Created       time.Time `json:"created"`
	Author        string    `json:"author"`
	Body          string    `json:"body"`
	Truncated     bool      `json:"truncated,omitempty"`
	Thread        string    `json:"thread,omitempty"`
	Message       string    `json:"message,omitempty"`
	Question      string    `json:"question,omitempty"`
	Label         string    `json:"label,omitempty"`
	ComparisonKey string    `json:"comparisonKey,omitempty"`
	Targets       []Target  `json:"targets,omitempty"`
	Conversation  *Target   `json:"conversation,omitempty"`
}

func HexID(v string, bytes int) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == bytes && strings.Trim(v, "0") != "" && strings.ToLower(v) == v
}
func (r Record) SpanID() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("brote.metadata.v1\x00%s\x00%s\x00%s\x00%d", r.TraceID, r.Kind, r.ID, r.Revision)))
	return hex.EncodeToString(h[:8])
}
func (r Record) Check() error {
	if r.Kind != "conversation" && r.Kind != "annotation" {
		return fmt.Errorf("invalid metadata kind")
	}
	if r.ID == "" || len(r.ID) > 256 || r.Revision == 0 || r.Session == "" || len(r.Session) > 200 || !HexID(r.TraceID, 16) || !HexID(r.ParentID, 8) || r.Created.IsZero() {
		return fmt.Errorf("invalid metadata identity")
	}
	if strings.TrimSpace(r.Body) == "" || len(r.Body) > MaxBody || !utf8.ValidString(r.Body) || r.Author == "" || len(r.Author) > 256 || len(r.Label) > 128 || len(r.ComparisonKey) > 128 || len(r.Targets) > 8 {
		return fmt.Errorf("invalid metadata content")
	}
	for _, t := range append(append([]Target{}, r.Targets...), optional(r.Conversation)...) {
		if t.Session == "" || len(t.Session) > 200 || !HexID(t.TraceID, 16) || (t.SpanID != "" && !HexID(t.SpanID, 8)) || len(t.CaptureID) > 256 {
			return fmt.Errorf("invalid evidence target")
		}
	}
	return nil
}
func optional(t *Target) []Target {
	if t == nil {
		return nil
	}
	return []Target{*t}
}
func BoundBody(s string) (string, bool) {
	if len(s) <= MaxBody {
		return s, false
	}
	s = s[:MaxBody]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s, true
}
