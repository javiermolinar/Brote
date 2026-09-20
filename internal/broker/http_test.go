package broker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"debug-handover/internal/session"
)

func TestHTTPBoundary(t *testing.T) {
	b := &broker{s: session.Descriptor{HTTP: "http://127.0.0.1:9876", Token: "secret"}}
	cases := []struct {
		host, token, origin string
		want                int
	}{{"evil.test", "secret", "", 403}, {"127.0.0.1:9876", "", "", 401}, {"127.0.0.1:9876", "secret", "http://evil.test", 403}, {"127.0.0.1:9876", "secret", "http://127.0.0.1:9876", 404}}
	for _, tt := range cases {
		r := httptest.NewRequest("POST", "http://"+tt.host+"/api/unknown", strings.NewReader(`{}`))
		if tt.token != "" {
			r.Header.Set("Authorization", "Bearer "+tt.token)
		}
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		b.handler().ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("%+v: got %d", tt, w.Code)
		}
	}
	r := httptest.NewRequest("GET", b.s.HTTP+"/", nil)
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("page missing or contains token")
	}
}

func TestStaleActionRejectedBeforeBackend(t *testing.T) {
	b := &broker{generation: 5}
	_, e := b.action(obj{"action": "continue", "generation": 4})
	if e == nil || !strings.Contains(e.Error(), "changed") {
		t.Fatal(e)
	}
}
