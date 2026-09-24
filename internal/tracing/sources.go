package tracing

import (
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type MetadataStatus struct {
	ID      string `json:"id"`
	TraceID string `json:"traceId"`
	SpanID  string `json:"spanId"`
	Local   string `json:"local"`
	Remote  string `json:"remote"`
	Error   string `json:"error,omitempty"`
}

func (e *engine) syncSource(id string) ([]MetadataStatus, error) {
	records, err := session.ReadTraceMetadata(id)
	if err != nil {
		return nil, err
	}
	statuses := make([]MetadataStatus, 0, len(records))
	for _, r := range records {
		result, err := e.metadata(r)
		status := MetadataStatus{ID: r.ID, TraceID: r.TraceID, SpanID: r.SpanID(), Local: result.Local, Remote: result.Remote}
		if err != nil {
			status.Local = "pending"
			status.Error = err.Error()
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}
func (e *engine) syncSources() {
	ids, err := session.TraceSources()
	if err != nil {
		return
	}
	for _, id := range ids {
		_, _ = e.syncSource(id)
	}
}
func (e *engine) sourceRequest(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Session string `json:"session"`
	}
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, errors.New("JSON required")
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		return nil, errors.New("invalid metadata source")
	}
	return e.syncSource(in.Session)
}

// SyncDiscussion wakes the shared service for a committed offline write. Failure
// is an export warning, never a reason to undo a locally committed message.
func SyncDiscussion(ctx context.Context, id string) ([]MetadataStatus, error) {
	endpoint, err := Ensure(ctx)
	if err != nil {
		return nil, err
	}
	var statuses []MetadataStatus
	err = Request(ctx, endpoint, "trace-metadata/sync", map[string]string{"session": id}, &statuses)
	return statuses, err
}
