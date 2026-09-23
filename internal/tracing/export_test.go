package tracing

import (
	"context"
	"encoding/pem"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteTLSHeaderPrecedenceAndNoAmbientLocalAuth(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/traces" || r.Header.Get("Authorization") != "Basic encoded=value" || r.Header.Get("X-Generic") != "" {
			t.Errorf("unexpected remote request: %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "X-Generic=forbidden")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "Authorization=Basic%20encoded%3Dvalue")
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", "")
	remote, err := remoteFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if remote.push(context.Background(), ptrace.NewTraces()) == nil {
		t.Fatal("untrusted TLS accepted")
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", path)
	remote, err = remoteFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.push(context.Background(), ptrace.NewTraces()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", server.URL+"/v1/traces")
	remote, err = remoteFromEnv()
	if err != nil || remote.endpoint != server.URL+"/v1/traces" {
		t.Fatal("trace-specific endpoint precedence", err)
	}
}
