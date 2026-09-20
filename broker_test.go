package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppendZedPreservesJSONC(t *testing.T) {
	cases := []string{
		"[\n // existing task\n {\"label\":\"mine\",\"url\":\"http://localhost\"}, // keep me\n]\n",
		"[ {\"label\":\"mine\",\"nested\": {\"a\":1,},} /* keep */ ]",
		"[]",
		"[ // empty\n]",
	}
	for _, original := range cases {
		t.Run(original, func(t *testing.T) {
			p := obj{"label": "test", "adapter": "Delve"}
			out, e := appendZedProfile([]byte(original), p)
			if e != nil {
				t.Fatal(e)
			}
			end := strings.LastIndex(original, "]")
			if !bytes.HasPrefix(out, []byte(original[:end])) {
				t.Fatalf("original content changed: %s", out)
			}
			// Re-appending validates the complete JSONC document and is idempotent.
			again, e := appendZedProfile(out, p)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(out, again) {
				t.Fatal("duplicate profile")
			}
		})
	}
}
func TestInvalidZedConfigRejected(t *testing.T) {
	for _, s := range []string{"{}", "[broken]", "[/*unclosed]"} {
		if _, e := appendZedProfile([]byte(s), obj{}); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
func TestDAPFraming(t *testing.T) {
	var buf bytes.Buffer
	want := obj{"type": "event", "event": "stopped", "body": obj{"reason": "断点"}}
	if e := writeDAP(&buf, want); e != nil {
		t.Fatal(e)
	}
	got, e := readDAP(bufio.NewReader(&buf))
	if e != nil {
		t.Fatal(e)
	}
	if pretty(got) != pretty(want) {
		t.Fatalf("round trip: %v", got)
	}
	for _, frame := range []string{"Content-Length: -1\r\n\r\n", "Content-Length: 999999999\r\n\r\n", "Content-Length: 3\r\n\r\n{}"} {
		if _, e := readDAP(bufio.NewReader(strings.NewReader(frame))); e == nil {
			t.Fatalf("accepted invalid framing: %q", frame)
		}
	}
}
func TestHTTPBoundary(t *testing.T) {
	b := &Broker{s: Session{HTTP: "http://127.0.0.1:9876", Token: "secret"}}
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
	b := &Broker{generation: 5}
	_, e := b.action(obj{"action": "continue", "generation": 4})
	if e == nil || !strings.Contains(e.Error(), "changed") {
		t.Fatal(e)
	}
}
func TestJSONSourceStringsRemainIntact(t *testing.T) {
	data := []byte(`[{"label":"x,]/*\\\"//","a":[1,2,],}]`)
	out, e := appendZedProfile(data, obj{"label": "another"})
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(out, []byte(`x,]/*\\\"//`)) {
		t.Fatal("string modified")
	}
	_ = json.Valid(out)
}
