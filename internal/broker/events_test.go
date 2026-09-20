package broker

import (
	"agentdebugger/internal/session"
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEReplayLiveAndExpiredCursor(t *testing.T) {
	b := &broker{s: session.Descriptor{Dir: t.TempDir(), Binding: &session.Binding{ID: "client", Revision: 1}}, owner: "browser", done: make(chan struct{})}
	if err := b.emit("ownership_changed", ""); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(b.events))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"?cursor=0&binding=client", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scan := bufio.NewScanner(resp.Body)
	next := func() session.Event {
		t.Helper()
		for scan.Scan() {
			if strings.HasPrefix(scan.Text(), "data: ") {
				var event session.Event
				if err := json.Unmarshal([]byte(scan.Text()[6:]), &event); err != nil {
					t.Fatal(err)
				}
				return event
			}
		}
		t.Fatalf("stream ended: %v", scan.Err())
		return session.Event{}
	}
	if next().ID != 1 {
		t.Fatal("missing replay")
	}
	b.mu.Lock()
	b.owner = "agent"
	err = b.emit("control_returned", "hello")
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if event := next(); event.ID != 2 || event.Note != "hello" {
		t.Fatal(event)
	}
	cancel()
	resp.Body.Close()
	for _, query := range []string{"cursor=99", "cursor=abc", "binding=wrong"} {
		r, e := http.Get(server.URL + "?" + query)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode < 400 {
			t.Fatal(query)
		}
	}
}

func TestExpiredEventCursor(t *testing.T) {
	b := &broker{s: session.Descriptor{Cursor: 300, Events: []session.Event{{ID: 45}}}}
	w := httptest.NewRecorder()
	b.events(w, httptest.NewRequest("GET", "http://127.0.0.1/api/events?cursor=1", nil))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}
