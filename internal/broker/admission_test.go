package broker

import (
	"agentdebugger/internal/dap"
	"agentdebugger/internal/session"
	"bufio"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceHealthAdmission(t *testing.T) {
	b := &broker{s: session.Descriptor{ID: "s", RunID: "run", ServiceVersion: 1, Version: 2, HTTP: "http://127.0.0.1:9876", Token: "secret"}}
	for _, tc := range []struct {
		route, token string
		want         int
	}{
		{"/api/health", "", 401},
		{"/api/health", "bad", 401},
		{"/api/health?session=other", "secret", 409},
		{"/api/health", "secret", 200},
		{"/api/action", "", 401},
		{"/api/events", "", 401},
	} {
		r := httptest.NewRequest("GET", b.s.HTTP+tc.route, nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		b.handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("credential leaked")
		}

	}
	// No backend is configured: a health probe cannot accidentally query Delve.
}
func TestDAPAdmissionBeforePeerReservation(t *testing.T) {
	for _, token := range []string{"bad", "secret"} {
		b := &broker{s: session.Descriptor{ID: "s", RunID: "r", ServiceVersion: 1, Token: "secret"}}
		server, client := net.Pipe()
		done := make(chan bool, 1)
		go func() {
			conn, ok := b.admitDAP(server)
			if conn != nil {
				conn.Close()
			}
			done <- ok
		}()
		if err := dap.Write(client, obj{"seq": 1, "type": "request", "command": "brote/authenticate", "arguments": obj{"session": "s", "run": "r", "token": token}}); err != nil {
			t.Fatal(err)
		}
		reply, err := dap.Read(bufio.NewReader(client))
		if err != nil {
			t.Fatal(err)
		}
		ok := <-done
		client.Close()
		if ok != (token == "secret") || reply["success"] != ok {
			t.Fatalf("wrong admission %v", reply)
		}
		if b.peer != nil || b.generation != 0 {
			t.Fatal("authentication mutated execution")
		}
	}
}

func TestServiceCannotEndForeignSession(t *testing.T) {
	b := &broker{s: session.Descriptor{ID: "own", RunID: "r", ServiceVersion: 1, HTTP: "http://127.0.0.1:9876", Token: "secret"}}
	r := httptest.NewRequest("POST", b.s.HTTP+"/api/sessions/stop", strings.NewReader(`{"id":"other","confirmed":true}`))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("foreign session operation: %d %s", w.Code, w.Body.String())
	}
}

func TestServiceCredentialDoesNotAuthorizeWorkspaceRoutes(t *testing.T) {
	b := &broker{s: session.Descriptor{ID: "own", RunID: "r", ServiceVersion: 1, HTTP: "http://127.0.0.1:9876", Token: "secret"}}
	for _, route := range []string{"/api/workspace", "/api/sessions", "/api/runs/start", "/api/saved-run?id=other"} {
		r := httptest.NewRequest("GET", b.s.HTTP+route, nil)
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		b.handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s: %d", route, w.Code)
		}
	}
}
