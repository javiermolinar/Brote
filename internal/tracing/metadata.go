package tracing

import (
	"agentdebugger/internal/traceinfo"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
)

// MetadataReceipt is persisted with the run, before enqueueing either export.
// Accepted means the destination acknowledged ingestion, not query visibility.
type MetadataReceipt struct {
	Record traceinfo.Record `json:"record"`
	Local  string           `json:"local"`
	Remote string           `json:"remote"`
}

func (e *engine) validateTarget(t traceinfo.Target, conversation bool) error {
	c, err := e.load(t.Session)
	if err != nil {
		return fmt.Errorf("evidence trace unavailable")
	}
	if conversation {
		if t.TraceID != c.Debugger || t.SpanID == "" {
			return errors.New("conversation trace mismatch")
		}
		r, ok := c.Metadata[t.SpanID]
		if !ok || r.Record.Kind != "conversation" {
			return errors.New("conversation span unavailable")
		}
		return nil
	}
	if t.TraceID != c.Program {
		return errors.New("program trace mismatch")
	}
	if t.CaptureID != "" {
		if c.Captures[t.CaptureID] == "" || c.Captures[t.CaptureID] != t.SpanID {
			return errors.New("capture span mismatch")
		}
		return nil
	}
	if t.SpanID == c.Run || c.ProgramRoots[t.SpanID] {
		return nil
	}
	for _, id := range c.Captures {
		if id == t.SpanID {
			return nil
		}
	}
	return errors.New("evidence span unavailable")
}
func (e *engine) metadata(r traceinfo.Record) (MetadataReceipt, error) {
	if err := r.Check(); err != nil {
		return MetadataReceipt{}, err
	}
	data, _ := json.Marshal(r)
	var owned traceinfo.Record
	_ = json.Unmarshal(data, &owned)
	r = owned
	e.mu.Lock()
	defer e.mu.Unlock()
	c, err := e.load(r.Session)
	if err != nil {
		return MetadataReceipt{}, errors.New("trace session unavailable")
	}
	key := r.SpanID()
	if old, ok := c.Metadata[key]; ok {
		if !reflect.DeepEqual(old.Record, r) {
			return MetadataReceipt{}, errors.New("metadata identity already used for different content")
		}
		e.enqueueMetadata(c, key)
		return old, nil
	}
	if len(c.Metadata) >= 4096 {
		return MetadataReceipt{}, errors.New("trace metadata limit reached")
	}
	if r.Kind == "conversation" {
		if r.TraceID != c.Debugger || (r.ParentID != c.Root && !c.DebuggerRoots[r.ParentID]) {
			return MetadataReceipt{}, errors.New("conversation owner mismatch")
		}
	} else if r.TraceID != c.Program || (r.ParentID != c.Run && !c.ProgramRoots[r.ParentID]) {
		return MetadataReceipt{}, errors.New("annotation owner mismatch")
	}
	for _, target := range r.Targets {
		if r.Kind == "annotation" && !sameInvestigation(r.Session, target.Session) {
			return MetadataReceipt{}, errors.New("annotation targets must belong to the same investigation")
		}
		if err = e.validateTarget(target, false); err != nil {
			return MetadataReceipt{}, err
		}
	}
	if r.Kind == "annotation" {
		own := false
		for _, target := range r.Targets {
			if target.Session == r.Session && target.TraceID == r.TraceID {
				own = true
			}
		}
		if !own {
			return MetadataReceipt{}, errors.New("annotation requires evidence in its owning trace")
		}
	}
	if r.Conversation != nil {
		if r.Kind == "annotation" && !sameInvestigation(r.Session, r.Conversation.Session) {
			return MetadataReceipt{}, errors.New("conversation belongs to another investigation")
		}
		if err = e.validateTarget(*r.Conversation, true); err != nil {
			return MetadataReceipt{}, err
		}
	}
	// Copy through JSON to remove caller-owned slices and time monotonic data.
	receipt := MetadataReceipt{Record: r, Local: "pending", Remote: "not_configured"}
	if e.remote != nil {
		receipt.Remote = "pending"
	} else if e.remoteError {
		receipt.Remote = "failed"
	}
	if c.Metadata == nil {
		c.Metadata = map[string]MetadataReceipt{}
	}
	c.Metadata[key] = receipt
	if r.Kind == "annotation" {
		c.ProgramSpans++
	} else {
		c.DebuggerSpans++
	}
	if err = e.save(c); err != nil {
		delete(c.Metadata, key)
		if r.Kind == "annotation" {
			c.ProgramSpans--
		} else {
			c.DebuggerSpans--
		}
		return MetadataReceipt{}, err
	}
	e.last = time.Now()
	e.enqueueMetadata(c, key)
	return receipt, nil
}
func (c *capture) metadataSpans(r traceinfo.Record) ptrace.Traces {
	s := c.span("brote."+r.Kind, r.Kind == "annotation", r.ParentID, r.Created)
	s.SetSpanID(spanID(r.SpanID()))
	s.SetEndTimestamp(s.StartTimestamp())
	a := s.Attributes()
	a.PutStr("brote."+r.Kind, r.Body)
	a.PutStr("brote.metadata.id", r.ID)
	a.PutInt("brote.metadata.revision", int64(r.Revision))
	a.PutStr("brote.metadata.kind", r.Kind)
	a.PutStr("brote.author", r.Author)
	a.PutBool("brote.body.truncated", r.Truncated)
	for k, v := range map[string]string{"brote.thread.id": r.Thread, "brote.message.id": r.Message, "brote.question.id": r.Question, "brote.label": r.Label, "brote.comparison.key": r.ComparisonKey} {
		if v != "" {
			a.PutStr(k, v)
		}
	}
	targets := append([]traceinfo.Target{}, r.Targets...)
	if r.Conversation != nil {
		targets = append(targets, *r.Conversation)
	}
	for _, t := range targets {
		l := s.Links().AppendEmpty()
		l.SetTraceID(traceID(t.TraceID))
		l.SetSpanID(spanID(t.SpanID))
		if t.CaptureID != "" {
			l.Attributes().PutStr("program.capture.id", t.CaptureID)
		}
	}
	return c.batch([]ptrace.Span{s})
}
func (e *engine) enqueueMetadata(c *capture, key string) {
	r := c.Metadata[key]
	if e.metadataFlight == nil {
		e.metadataFlight = map[string]bool{}
	}
	for destination, q := range map[string]chan batch{"local": e.local, "remote": e.remote} {
		status := r.Local
		if destination == "remote" {
			status = r.Remote
		}
		token := c.Session + "/" + key + "/" + destination
		if q == nil || status == "accepted" || e.metadataFlight[token] {
			continue
		}
		select {
		case q <- batch{session: c.Session, traces: c.metadataSpans(r.Record), metadata: key}:
			e.metadataFlight[token] = true
		default:
		}
	}
}

// RetryMetadata is also called on service startup, so crash-pending records do
// not require the originating debugger or CLI to remain alive.
func (e *engine) retryMetadata() {
	e.mu.Lock()
	defer e.mu.Unlock()
	paths, _ := filepath.Glob(filepath.Join(e.dir, "sessions", "*.json"))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		var r Record
		if err != nil || json.Unmarshal(data, &r) != nil {
			continue
		}
		c, err := e.load(r.Session)
		if err != nil {
			continue
		}
		for key := range c.Metadata {
			e.enqueueMetadata(c, key)
		}
	}
}
func (e *engine) metadataRequest(w http.ResponseWriter, r *http.Request) (any, error) {
	var in traceinfo.Record
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, errors.New("JSON required")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return nil, errors.New("invalid metadata request")
	}
	return e.metadata(in)
}
