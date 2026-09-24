package broker

import (
	"agentdebugger/internal/tracing"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Annotation writes are metadata-only. They never acquire execution ownership
// or use a paused debugger to manufacture a target.
func (b *broker) annotationRequest(w http.ResponseWriter, r *http.Request) (any, error) {
	b.mu.Lock()
	id := b.s.ID
	b.mu.Unlock()
	if r.URL.Path == "/api/annotation-evidence" {
		if r.Method != "GET" {
			return nil, errors.New("method not allowed")
		}
		return tracing.EvidenceFor(r.Context(), id)
	}
	owner := r.URL.Query().Get("traceSession")
	var in tracing.AnnotationRequest
	if r.Method == "POST" {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil {
			return nil, errors.New("invalid annotation request")
		}
		owner = in.Session
	} else if r.Method != "GET" {
		return nil, errors.New("method not allowed")
	}
	if owner != id && !strings.HasPrefix(owner, id+":") {
		return nil, errors.New("annotation session identity mismatch")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if r.Method == "POST" {
		return tracing.Annotate(ctx, in)
	}
	return tracing.Annotations(ctx, owner)
}
