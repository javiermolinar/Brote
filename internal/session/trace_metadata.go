package session

import (
	"agentdebugger/internal/traceinfo"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// TraceOwner is the persisted publication destination, independent of evidence
// provenance. It remains available for replies after the debugger exits.
type TraceOwner struct {
	Session  string `json:"session"`
	Debugger string `json:"debugger"`
	Program  string `json:"program"`
	Root     string `json:"root"`
	Run      string `json:"run"`
}

func (o TraceOwner) Valid() bool {
	return o.Session != "" && traceinfo.HexID(o.Debugger, 16) && traceinfo.HexID(o.Program, 16) && traceinfo.HexID(o.Root, 8) && traceinfo.HexID(o.Run, 8)
}

func traceSourcesDir() (string, error) {
	root, err := DataRoot()
	return filepath.Join(root, "trace-sources"), err
}
func registerTraceSource(id string) error {
	if !discussionID.MatchString(id) {
		return fmt.Errorf("invalid trace source session")
	}
	dir, err := traceSourcesDir()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return Write(filepath.Join(dir, id+".json"), id)
}

// TraceSources names authoritative local documents, not copied message payloads.
// Registering before the document write cannot publish an uncommitted message.
func TraceSources() ([]string, error) {
	dir, err := traceSourcesDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") && discussionID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func ReadTraceMetadata(id string) ([]traceinfo.Record, error) {
	d, err := ReadDiscussion(id)
	if err != nil {
		return nil, err
	}
	var records []traceinfo.Record
	for _, t := range d.Threads {
		for _, m := range t.Messages {
			if m.Trace != nil && strings.HasPrefix(m.Trace.ID, id+"/") {
				records = append(records, *m.Trace)
			}
		}
	}
	return records, nil
}
func contextTargets(context map[string]any) []traceinfo.Target {
	if context == nil {
		return nil
	}
	var ids struct {
		Program string `json:"programTraceId"`
		Root    string `json:"programRootId"`
	}
	data, _ := json.Marshal(context["traceIds"])
	_ = json.Unmarshal(data, &ids)
	sessionID, _ := context["session"].(string)
	run, _ := context["run"].(string)
	if traceinfo.HexID(ids.Program, 16) && traceinfo.HexID(ids.Root, 8) && sessionID != "" && run != "" {
		target := traceinfo.Target{Session: sessionID + ":" + run, TraceID: ids.Program, SpanID: ids.Root}
		capture, _ := context["captureId"].(string)
		span, _ := context["programSpanId"].(string)
		if capture != "" && traceinfo.HexID(span, 8) {
			target.CaptureID = capture
			target.SpanID = span
		}
		return []traceinfo.Target{target}
	}
	var native struct {
		Record struct {
			Session string
			Program string
			Run     string
		}
	}
	data, _ = json.Marshal(context["traces"])
	_ = json.Unmarshal(data, &native)
	if native.Record.Session != "" && traceinfo.HexID(native.Record.Program, 16) && traceinfo.HexID(native.Record.Run, 8) {
		return []traceinfo.Target{{Session: native.Record.Session, TraceID: native.Record.Program, SpanID: native.Record.Run}}
	}
	return nil
}

// prepareConversationTraces runs inside the discussion CAS transaction. Existing
// messages are immutable, while copied history retains its original ownership.
func prepareConversationTraces(current Discussion, next *Discussion) error {
	previous := map[string]CommentMessage{}
	for _, t := range current.Threads {
		for _, m := range t.Messages {
			previous[t.ID+"/"+m.ID] = m
		}
	}
	for ti := range next.Threads {
		t := &next.Threads[ti]
		for mi := range t.Messages {
			m := &t.Messages[mi]
			key := t.ID + "/" + m.ID
			if old, ok := previous[key]; ok {
				if old.Trace != nil && (m.Body != old.Body || m.Author != old.Author || m.Created != old.Created || !reflect.DeepEqual(m.Trace, old.Trace)) {
					return fmt.Errorf("exported discussion message is immutable")
				}
				continue
			}
			// Preserve imported/continued records; never assign their old content a new owner.
			if m.Trace != nil || (m.Run != "" && m.Run != next.Session) || next.TraceOwner == nil || !next.TraceOwner.Valid() {
				continue
			}
			created, err := time.Parse(time.RFC3339Nano, m.Created)
			if err != nil {
				continue
			} // legacy messages have no trustworthy timestamp
			body, truncated := traceinfo.BoundBody(m.Body)
			r := traceinfo.Record{ID: next.Session + "/" + key, Revision: 1, Kind: "conversation", Session: next.TraceOwner.Session, TraceID: next.TraceOwner.Debugger, ParentID: next.TraceOwner.Root, Created: created, Author: m.Author, Body: body, Truncated: truncated, Thread: t.ID, Message: m.ID, Question: m.Question, Targets: contextTargets(m.Context)}
			hasEvidence := m.Context != nil || m.Evidence != nil
			if m.Question != "" {
				for _, question := range t.Messages {
					if question.ID == m.Question {
						hasEvidence = hasEvidence || question.Context != nil || question.Evidence != nil
						r.Targets = contextTargets(question.Context)
						if question.Trace != nil {
							r.Targets = append([]traceinfo.Target{}, question.Trace.Targets...)
						}
						break
					}
				}
			}
			if len(r.Targets) == 0 && !hasEvidence {
				r.Targets = []traceinfo.Target{{Session: next.TraceOwner.Session, TraceID: next.TraceOwner.Program, SpanID: next.TraceOwner.Run}}
			}
			if err := r.Check(); err != nil {
				return err
			}
			m.Trace = &r
		}
	}
	return nil
}
