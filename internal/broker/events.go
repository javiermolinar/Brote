package broker

import (
	"agentdebugger/internal/session"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// events streams durable events, replaying after a cursor. It never polls Delve.
func (b *broker) events(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	if raw == "" {
		raw = "0"
	}
	cursor, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid cursor"}`, 400)
		return
	}
	binding := r.URL.Query().Get("binding")
	b.mu.Lock()
	if cursor > b.s.Cursor || (len(b.s.Events) > 0 && cursor < b.s.Events[0].ID-1) {
		b.mu.Unlock()
		http.Error(w, `{"error":"cursor expired; read state and reconnect using its cursor"}`, 409)
		return
	}
	if binding != "" && (b.s.Binding == nil || binding != b.s.Binding.ID) {
		b.mu.Unlock()
		http.Error(w, `{"error":"binding changed"}`, 409)
		return
	}
	if binding != "" {
		b.agentStreams++
		b.agentDisconnected = time.Time{}
	}
	b.mu.Unlock()
	if binding != "" {
		defer func() {
			b.mu.Lock()
			b.agentStreams--
			if b.agentStreams == 0 {
				b.agentDisconnected = time.Now()
			}
			b.mu.Unlock()
		}()
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		b.mu.Lock()
		if b.changed == nil {
			b.changed = make(chan struct{})
		}
		changed := b.changed
		if len(b.s.Events) > 0 && cursor < b.s.Events[0].ID-1 {
			b.mu.Unlock()
			fmt.Fprint(w, "event: reset\ndata: {\"reset\":true}\n\n")
			flusher.Flush()
			return
		}
		pending := []session.Event{}
		for _, event := range b.s.Events {
			if event.ID > cursor {
				pending = append(pending, event)
			}
		}
		b.mu.Unlock()
		for _, event := range pending {
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, data); err != nil {
				return
			}
			flusher.Flush()
			cursor = event.ID
			if event.Kind == "terminated" || (binding != "" && event.Binding != nil && event.Binding.ID != binding) {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-b.done:
			return
		case <-changed:
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
