package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalEndpoint(t *testing.T) {
	for _, v := range []string{"http://example.com:80", "http://127.0.0.1:80@evil.test", "http://127.0.0.1:80/api", "http://127.0.0.1:80?token=x", "http://127.0.0.1:80#x", "https://127.0.0.1:80"} {
		if LocalEndpoint(v) == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	if err := LocalEndpoint("http://127.0.0.1:1234"); err != nil {
		t.Fatal(err)
	}
}
func TestServiceDiscoveryHealthAndRunIsolation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", root)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" || r.Method != "GET" {
			t.Error("discovery contacted debugger endpoint", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing credential")
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "s", "run": "live", "serviceVersion": 1, "status": "connected"})
	}))
	defer server.Close()
	dir := filepath.Join(root, "s")
	os.Mkdir(dir, 0700)
	s := Descriptor{ID: "s", ServiceVersion: 1, Version: 2, RunID: "old", Token: "secret", HTTP: server.URL}
	if err := Write(filepath.Join(dir, "session.json"), s); err != nil {
		t.Fatal(err)
	}
	list, err := List(context.Background())
	if err != nil || len(list) != 1 || list[0].Status != "offline" {
		t.Fatalf("accepted wrong run %v %v", list, err)
	}
	s.RunID = "live"
	Write(filepath.Join(dir, "session.json"), s)
	list, err = List(context.Background())
	if err != nil || list[0].Status != "connected" {
		t.Fatalf("missing session %v %v", list, err)
	}
	data, _ := json.Marshal(list)
	if string(data) == "" {
		t.Fatal("empty discovery")
	}
	if list[0].Panel != server.URL+"/" {
		t.Fatal("credential appeared in discovery", list)
	}
}

func TestDescriptorCompatibility(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEBUG_HANDOVER_HOME", root)
	for _, tc := range []struct {
		name  string
		value Descriptor
		valid bool
	}{
		{"legacy", Descriptor{ID: "legacy", Version: 2}, true},
		{"service", Descriptor{ID: "service", Version: 2, ServiceVersion: 1, RunID: "r", Token: "secret"}, true},
		{"future", Descriptor{ID: "future", Version: 2, ServiceVersion: 2, RunID: "r", Token: "secret"}, false},
		{"incomplete", Descriptor{ID: "incomplete", Version: 2, ServiceVersion: 1}, false},
		{"mismatched", Descriptor{ID: "different", Version: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, tc.name)
			os.Mkdir(dir, 0700)
			path := filepath.Join(dir, "session.json")
			if err := Write(path, tc.value); err != nil {
				t.Fatal(err)
			}
			_, err := Read(tc.name)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			st, err := os.Stat(path)
			if err != nil || st.Mode().Perm() != 0600 {
				t.Fatal("descriptor is not private")
			}
		})
	}
}
