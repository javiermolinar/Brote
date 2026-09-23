package cli

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceErrorCodeSurvivesClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		w.Write([]byte(`{"version":1,"code":"stale_revision","error":"stale definition revision"}`))
	}))
	defer server.Close()
	_, err := api(session.Descriptor{HTTP: server.URL, ServiceVersion: 1}, "GET", "/api/v1", nil)
	var p *protocol.Error
	if !errors.As(err, &p) || p.Code != "stale_revision" {
		t.Fatalf("structured error lost: %v", err)
	}
}
