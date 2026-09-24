package tracing

import (
	"agentdebugger/internal/session"
	"agentdebugger/internal/traceinfo"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// AnnotationRequest identifies exact evidence. Clients retain ID and Revision
// across retries. The service never invents labels or findings.
type AnnotationRequest struct {
	Session       string             `json:"session"` // owning tracing-service session, not broker ID
	ID            string             `json:"id"`
	Revision      uint64             `json:"revision"`
	Author        string             `json:"author"`
	Body          string             `json:"body"`
	Label         string             `json:"label,omitempty"`
	ComparisonKey string             `json:"comparisonKey,omitempty"`
	Targets       []traceinfo.Target `json:"targets"`
	Conversation  *traceinfo.Target  `json:"conversation,omitempty"`
}
type Annotation struct {
	ConversationThread string `json:"conversationThread,omitempty"`
	ConversationRun    string `json:"conversationRun,omitempty"`
	AnnotationRequest
	Created time.Time          `json:"created"`
	Records []traceinfo.Record `json:"records"`
	Export  []MetadataStatus   `json:"export,omitempty"`
}

func annotationPath(dir, id string) string {
	return filepath.Join(dir, "annotations", fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))))
}
func (e *engine) readAnnotations(owner string) ([]Annotation, error) {
	data, err := os.ReadFile(annotationPath(e.dir, owner))
	if os.IsNotExist(err) {
		return []Annotation{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Annotation
	err = json.Unmarshal(data, &list)
	return list, err
}
func sameInvestigation(a, b string) bool {
	if a == b {
		return true
	}
	a = strings.SplitN(a, ":", 2)[0]
	b = strings.SplitN(b, ":", 2)[0]
	x, err := session.InvestigationFor(a)
	if err != nil {
		return false
	}
	y, err := session.InvestigationFor(b)
	return err == nil && x == y
}
func (e *engine) prepareAnnotation(in AnnotationRequest) (Annotation, error) {
	if err := validSession(in.Session); err != nil {
		return Annotation{}, err
	}
	if in.ID == "" || len(in.ID) > 128 || in.Revision == 0 || len(in.Targets) == 0 || len(in.Targets) > 8 {
		return Annotation{}, errors.New("annotation requires id, revision, and 1–8 evidence targets")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	list, err := e.readAnnotations(in.Session)
	if err != nil {
		return Annotation{}, err
	}
	latest := uint64(0)
	for _, a := range list {
		if a.ID == in.ID {
			if a.Revision == in.Revision {
				if reflect.DeepEqual(a.AnnotationRequest, in) {
					return a, nil
				}
				return Annotation{}, errors.New("annotation identity already used")
			}
			if a.Revision > latest {
				latest = a.Revision
			}
		}
	}
	if latest+1 != in.Revision {
		return Annotation{}, errors.New("annotation revision is stale or skips a revision")
	}
	if len(list) >= 1000 {
		return Annotation{}, errors.New("annotation revision limit reached")
	}
	// Validate every target before persisting any part of a comparison pair.
	owners := map[string]*capture{}
	own := false
	for _, t := range in.Targets {
		if !sameInvestigation(in.Session, t.Session) {
			return Annotation{}, errors.New("annotation targets must belong to the same investigation")
		}
		if t.Session == in.Session {
			own = true
		}
		if err := e.validateTarget(t, false); err != nil {
			return Annotation{}, err
		}
		c, _ := e.load(t.Session)
		owners[t.Session] = c
	}
	if !own {
		return Annotation{}, errors.New("annotation must target its owning session")
	}
	if in.Conversation != nil {
		if !sameInvestigation(in.Session, in.Conversation.Session) {
			return Annotation{}, errors.New("conversation belongs to another investigation")
		}
		if err := e.validateTarget(*in.Conversation, true); err != nil {
			return Annotation{}, err
		}
	}
	a := Annotation{AnnotationRequest: in, Created: time.Now().UTC()}
	keys := make([]string, 0, len(owners))
	for k := range owners {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		c := owners[key]
		r := traceinfo.Record{ID: in.Session + "/" + in.ID, Revision: in.Revision, Kind: "annotation", Session: c.Session, TraceID: c.Program, ParentID: c.Run, Created: a.Created, Author: in.Author, Body: in.Body, Label: in.Label, ComparisonKey: in.ComparisonKey, Targets: in.Targets, Conversation: in.Conversation}
		if err := r.Check(); err != nil {
			return Annotation{}, err
		}
		if len(c.Metadata) >= 4096 {
			return Annotation{}, errors.New("trace metadata limit reached")
		}
		a.Records = append(a.Records, r)
	}
	if err = os.MkdirAll(filepath.Join(e.dir, "annotations"), 0700); err != nil {
		return Annotation{}, err
	}
	if err = session.Write(annotationPath(e.dir, in.Session), append(list, a)); err != nil {
		return Annotation{}, err
	}
	return a, nil
}
func (e *engine) publishAnnotation(a Annotation) Annotation {
	if a.Conversation != nil {
		e.mu.Lock()
		if c, err := e.load(a.Conversation.Session); err == nil {
			if m, ok := c.Metadata[a.Conversation.SpanID]; ok && m.Record.Kind == "conversation" {
				a.ConversationThread = m.Record.Thread
				a.ConversationRun = strings.SplitN(a.Conversation.Session, ":", 2)[0]
			}
		}
		e.mu.Unlock()
	}

	a.Export = make([]MetadataStatus, 0, len(a.Records))
	for _, r := range a.Records {
		receipt, err := e.metadata(r)
		status := MetadataStatus{ID: r.ID, SpanID: r.SpanID(), TraceID: r.TraceID, Local: receipt.Local, Remote: receipt.Remote}
		if err != nil {
			status.Local = "pending"
			status.Error = err.Error()
		}
		a.Export = append(a.Export, status)
	}
	return a
}
func (e *engine) annotate(in AnnotationRequest) (Annotation, error) {
	a, err := e.prepareAnnotation(in)
	if err != nil {
		return Annotation{}, err
	}
	return e.publishAnnotation(a), nil
}
func (e *engine) syncAnnotations() {
	e.mu.Lock()
	paths, _ := filepath.Glob(filepath.Join(e.dir, "annotations", "*.json"))
	var all []Annotation
	for _, p := range paths {
		data, err := os.ReadFile(p)
		var list []Annotation
		if err == nil && json.Unmarshal(data, &list) == nil {
			all = append(all, list...)
		}
	}
	e.mu.Unlock()
	for _, a := range all {
		e.publishAnnotation(a)
	}
}
func (e *engine) listAnnotations(owner string) ([]Annotation, error) {
	if err := validSession(owner); err != nil {
		return nil, err
	}
	e.mu.Lock()
	paths, _ := filepath.Glob(filepath.Join(e.dir, "annotations", "*.json"))
	var result []Annotation
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		var list []Annotation
		if err = json.Unmarshal(data, &list); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		latest := map[string]Annotation{}
		for _, a := range list {
			if old, ok := latest[a.ID]; !ok || a.Revision > old.Revision {
				latest[a.ID] = a
			}
		}
		for _, a := range latest {
			for _, r := range a.Records {
				if r.Session == owner {
					result = append(result, a)
					break
				}
			}
		}
	}
	e.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].Created.After(result[j].Created) })
	for i := range result {
		result[i] = e.publishAnnotation(result[i])
	}
	if result == nil {
		result = []Annotation{}
	}
	return result, nil
}
func (e *engine) annotationRequest(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method == "GET" {
		return e.listAnnotations(r.URL.Query().Get("session"))
	}
	var in AnnotationRequest
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, errors.New("JSON required")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		return nil, errors.New("invalid annotation request")
	}
	return e.annotate(in)
}

// Annotate and Annotations are shared by CLI and browser adapters. A successful
// Annotate response means the local note is saved even when Export is pending.
func Annotate(ctx context.Context, in AnnotationRequest) (Annotation, error) {
	endpoint, err := Ensure(ctx)
	if err != nil {
		return Annotation{}, err
	}
	var a Annotation
	err = Request(ctx, endpoint, "annotations", in, &a)
	return a, err
}
func Annotations(ctx context.Context, owner string) ([]Annotation, error) {
	endpoint, err := Ensure(ctx)
	if err != nil {
		return nil, err
	}
	var a []Annotation
	err = Request(ctx, endpoint, "annotations?session="+url.QueryEscape(owner), nil, &a)
	return a, err
}
