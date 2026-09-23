// Package tracing owns Brote's trace schema, capture, export and storage API.
package tracing

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

type Frame struct {
	Name   string `json:"name,omitempty"`
	Line   int    `json:"line,omitempty"`
	Source *struct {
		Path string `json:"path"`
	} `json:"source,omitempty"`
}
type Observation struct {
	Thread     int               `json:"thread"`
	Frame      Frame             `json:"frame"`
	Stack      []Frame           `json:"stack"`
	Scopes     []json.RawMessage `json:"scopes"`
	CapturedAt time.Time         `json:"capturedAt"`
}
type Event struct {
	Incomplete  bool              `json:"incomplete,omitempty"`
	At          time.Time         `json:"at,omitempty"`
	Session     string            `json:"session"`
	Name        string            `json:"name,omitempty"`
	Adapter     string            `json:"adapter,omitempty"`
	Kind        string            `json:"kind"`
	Seq         int               `json:"seq,omitempty"`
	Command     string            `json:"command,omitempty"`
	Thread      int               `json:"thread,omitempty"`
	Success     bool              `json:"success,omitempty"`
	AllThreads  bool              `json:"allThreads,omitempty"`
	Observation *Observation      `json:"observation,omitempty"`
	Label       string            `json:"label,omitempty"`
	Selections  map[string]string `json:"selections,omitempty"`
	Data        json.RawMessage   `json:"data,omitempty"`
}
type Record struct {
	External      bool              `json:"external,omitempty"`
	Incomplete    bool              `json:"incomplete,omitempty"`
	Interrupted   bool              `json:"interrupted,omitempty"`
	ProgramSpans  int               `json:"programSpans"`
	DebuggerSpans int               `json:"debuggerSpans"`
	LastSeen      time.Time         `json:"lastSeen"`
	Sequence      int               `json:"sequence"`
	Session       string            `json:"session"`
	Name          string            `json:"name"`
	Adapter       string            `json:"adapter"`
	Program       string            `json:"program"`
	Debugger      string            `json:"debugger"`
	Local         map[string]string `json:"local"`
	Remote        map[string]string `json:"remote,omitempty"`
	Closed        bool              `json:"closed"`
	Started       time.Time         `json:"started"`
	Run           string            `json:"run"`
	Root          string            `json:"root"`
}
type pending struct {
	span      ptrace.Span
	execution bool
	thread    int
	ack       bool
	stop      time.Time
	outcome   string
}
type capture struct {
	Record
	requests map[int]*pending
	threads  map[int]ptrace.Span
	sequence int
}

func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func newCapture(e Event) *capture {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	return &capture{Record: Record{Session: e.Session, Name: bounded(e.Name, 512), Adapter: bounded(e.Adapter, 128), Program: newID(16), Debugger: newID(16), Run: newID(8), Root: newID(8), Started: e.At, Local: map[string]string{}, Remote: map[string]string{}}, requests: map[int]*pending{}, threads: map[int]ptrace.Span{}}
}
func bounded(v string, n int) string {
	if len(v) <= n {
		return v
	}
	v = v[:n]
	for !utf8.ValidString(v) {
		v = v[:len(v)-1]
	}
	return v
}
func traceID(s string) pcommon.TraceID {
	var v pcommon.TraceID
	b, _ := hex.DecodeString(s)
	copy(v[:], b)
	return v
}
func spanID(s string) pcommon.SpanID {
	var v pcommon.SpanID
	b, _ := hex.DecodeString(s)
	copy(v[:], b)
	return v
}
func (c *capture) span(name string, program bool, parent string, at time.Time) ptrace.Span {
	s := ptrace.NewSpan()
	s.SetName(bounded(name, 512))
	s.SetSpanID(spanID(newID(8)))
	s.SetParentSpanID(spanID(parent))
	s.SetStartTimestamp(pcommon.NewTimestampFromTime(at))
	s.SetTraceID(traceID(c.Debugger))
	s.Attributes().PutStr("debugger.session.id", c.Session)
	if program {
		s.SetTraceID(traceID(c.Program))
		s.Attributes().PutStr("program.run.id", c.Session)
		s.Attributes().PutInt("program.schema.version", 2)
	}
	return s
}
func (c *capture) finish(seq int, outcome string, at time.Time) ptrace.Span {
	p := c.requests[seq]
	delete(c.requests, seq)
	p.span.SetEndTimestamp(pcommon.NewTimestampFromTime(at))
	p.span.Attributes().PutStr("debugger.outcome", outcome)
	if outcome == "error" {
		p.span.Status().SetCode(ptrace.StatusCodeError)
	}
	return p.span
}

var executions = map[string]bool{"continue": true, "next": true, "stepIn": true, "stepOut": true, "restart": true}
var commands = map[string]bool{"launch": true, "attach": true, "pause": true, "disconnect": true, "terminate": true, "setBreakpoints": true, "setFunctionBreakpoints": true, "setExceptionBreakpoints": true, "evaluate": true}

func (c *capture) event(e Event) []ptrace.Span {
	if c.Closed {
		return nil
	}
	if c.External {
		if e.Kind == "close" {
			c.Closed = true
		}
		return nil
	}
	now := e.At
	if now.IsZero() {
		now = time.Now()
	}
	var out []ptrace.Span
	switch e.Kind {
	case "request":
		if len(c.requests) >= 1024 || (!executions[e.Command] && !commands[e.Command]) {
			break
		}
		if _, ok := c.requests[e.Seq]; ok {
			break
		}
		s := c.span(e.Command, false, c.Root, now)
		s.Attributes().PutStr("debugger.command", e.Command)
		s.Attributes().PutStr("debugger.channel", "dap")
		c.requests[e.Seq] = &pending{span: s, execution: executions[e.Command], thread: e.Thread}
	case "response":
		if p := c.requests[e.Seq]; p != nil {
			if !e.Success {
				out = append(out, c.finish(e.Seq, "error", now))
			} else {
				p.ack = true
				if !p.execution {
					out = append(out, c.finish(e.Seq, "success", now))
				} else if !p.stop.IsZero() {
					out = append(out, c.finish(e.Seq, p.outcome, p.stop))
				}
			}
		}
	case "stopped", "exited":
		for seq, p := range c.requests {
			if !p.execution || !p.stop.IsZero() || (!e.AllThreads && e.Kind != "exited" && p.thread != e.Thread) {
				continue
			}
			p.stop = now
			p.outcome = e.Kind
			if p.ack {
				out = append(out, c.finish(seq, e.Kind, now))
			}
		}
	case "snapshot":
		if e.Observation != nil {
			out = append(out, c.snapshot(e)...)
		}
	case "close":
		for seq := range c.requests {
			out = append(out, c.finish(seq, "interrupted", now))
		}
		for _, s := range c.threads {
			s.Attributes().PutStr("program.observation.boundary", "session-ended")
			s.SetEndTimestamp(pcommon.NewTimestampFromTime(now))
			out = append(out, s)
		}
		root := c.span("debugger.session", false, "", c.Started)
		root.SetSpanID(spanID(c.Root))
		root.SetEndTimestamp(pcommon.NewTimestampFromTime(now))
		out = append(out, root)
		run := c.span("run "+c.Name, true, "", c.Started)
		run.SetSpanID(spanID(c.Run))
		run.Attributes().PutStr("program.span.type", "run")
		run.SetEndTimestamp(pcommon.NewTimestampFromTime(now))
		link := run.Links().AppendEmpty()
		link.SetTraceID(traceID(c.Debugger))
		link.SetSpanID(spanID(c.Root))
		out = append(out, run)
		c.Closed = true
	case "record":
		s := c.span(e.Command, false, c.Root, now)
		s.Attributes().PutStr("brote.event.json", bounded(string(e.Data), 32768))
		s.SetEndTimestamp(s.StartTimestamp())
		out = append(out, s)
	}
	return out
}
func (c *capture) snapshot(e Event) []ptrace.Span {
	o := *e.Observation
	if c.sequence >= 1000 || o.CapturedAt.IsZero() {
		return nil
	}
	s, ok := c.threads[o.Thread]
	if !ok {
		if len(c.threads) >= 256 {
			return nil
		}
		entry := o.Frame.Name
		if len(o.Stack) > 0 {
			entry = o.Stack[len(o.Stack)-1].Name
		}
		s = c.span("thread observed at "+entry, true, c.Run, o.CapturedAt)
		s.Attributes().PutStr("program.span.type", "thread")
		s.Attributes().PutInt("program.thread.id", int64(o.Thread))
		s.Attributes().PutStr("program.observation.boundary", "first-observed")
		c.threads[o.Thread] = s
	}
	if len(o.Stack) > 30 {
		o.Stack = o.Stack[:30]
	}
	o.Scopes = append([]json.RawMessage(nil), o.Scopes...)
	partial := false
	for _, scope := range o.Scopes {
		var v struct {
			Omitted   string
			Truncated bool
			Variables []struct{ Truncated bool }
		}
		_ = json.Unmarshal(scope, &v)
		partial = partial || v.Omitted != "" || v.Truncated
		for _, v := range v.Variables {
			partial = partial || v.Truncated
		}
	}
	data, _ := json.Marshal(o)
	for len(data) > 32768 && len(o.Scopes) > 0 {
		o.Scopes = o.Scopes[:len(o.Scopes)-1]
		partial = true
		data, _ = json.Marshal(o)
	}
	if len(data) > 32768 {
		o.Stack = nil
		partial = true
		data, _ = json.Marshal(o)
	}
	if len(data) > 32768 {
		return nil
	}
	name := e.Label
	if name == "" {
		name = o.Frame.Name
	}
	if name == "" {
		name = "capture"
	}
	sp := c.span(name, true, s.SpanID().String(), o.CapturedAt)
	sp.SetEndTimestamp(sp.StartTimestamp())
	a := sp.Attributes()
	a.PutStr("program.span.type", "snapshot")
	a.PutInt("program.thread.id", int64(o.Thread))
	c.sequence++
	c.Sequence = c.sequence
	a.PutInt("program.snapshot.sequence", int64(c.sequence))
	status := "complete"
	if partial {
		status = "partial"
	}
	a.PutStr("program.capture.status", status)
	a.PutStr("program.snapshot.json", string(data))
	a.PutStr("code.function.name", bounded(o.Frame.Name, 512))
	a.PutInt("code.line.number", int64(o.Frame.Line))
	if o.Frame.Source != nil {
		a.PutStr("code.file.path", bounded(o.Frame.Source.Path, 2048))
	}
	if e.Label != "" {
		a.PutStr("program.capture.name", bounded(e.Label, 128))
	}
	count := 0
	for alias, selected := range e.Selections {
		if count >= 16 {
			break
		}
		if !aliasPattern.MatchString(alias) {
			continue
		}
		count++
		var matches []struct {
			Name               string
			Value              string
			Type               string
			Truncated          bool
			VariablesReference int
		}
		for _, scope := range o.Scopes {
			var v struct {
				Variables []struct {
					Name               string
					Value              string
					Type               string
					Truncated          bool
					VariablesReference int
				}
			}
			_ = json.Unmarshal(scope, &v)
			for _, v := range v.Variables {
				if v.Name == selected {
					matches = append(matches, v)
				}
			}
		}
		status := "not_captured"
		if len(matches) > 1 {
			status = "ambiguous"
		} else if len(matches) == 1 {
			v := matches[0]
			switch {
			case v.Truncated:
				status = "truncated"
			case v.VariablesReference != 0:
				status = "non_scalar"
			default:
				status = "available"
				a.PutStr("program.value."+alias, bounded(v.Value, 65536))
			}
			a.PutStr("program.value_type."+alias, bounded(v.Type, 512))
		}
		a.PutStr("program.value_status."+alias, status)
		a.PutStr("program.value_expression."+alias, bounded(selected, 512))
	}
	return []ptrace.Span{sp}
}
func (c *capture) batch(spans []ptrace.Span) ptrace.Traces {
	t := ptrace.NewTraces()
	for _, s := range spans {
		rs := t.ResourceSpans().AppendEmpty()
		service := "brote"
		scope := "brote.debugger"
		if s.TraceID() == traceID(c.Program) {
			service = c.Name
			scope = "brote.program"
		}
		rs.Resource().Attributes().PutStr("service.name", service)
		rs.Resource().Attributes().PutStr("debugger.adapter.type", c.Adapter)
		ss := rs.ScopeSpans().AppendEmpty()
		ss.Scope().SetName(scope)
		s.CopyTo(ss.Spans().AppendEmpty())
	}
	return t
}
func validSession(id string) error {
	if id == "" || len(id) > 200 {
		return fmt.Errorf("invalid session ID")
	}
	return nil
}
