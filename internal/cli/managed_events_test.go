package cli

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestManagedTransportUsesServiceClaimsAndReportsHostOutcome(t *testing.T) {
	var mu sync.Mutex
	claimed, receiptSeen := false, false
	r := protocol.Recipient{Kind: "agent", ID: "pi", Revision: 1}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "GET" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, ": connected\n\n")
			w.(http.Flusher).Flush()
			<-req.Context().Done()
			return
		}
		var a obj
		if err := json.NewDecoder(req.Body).Decode(&a); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		v := obj{}
		switch a["action"] {
		case "consumer-open":
			v["consumer"] = session.Consumer{ID: "host", Instance: "instance", Recipient: r}
		case "consumer-challenge":
			v["challenge"] = "challenge"
		case "consumer-close":
		case "consumer-next":
			if !claimed {
				claimed = true
				v["delivery"] = protocol.DeliveryEnvelope{Version: 1, Session: "0123456789", Run: "run", Kind: "handback", Subject: "1", Attempt: "a", Recipient: r, Status: "sending", Message: "canonical Go message"}
			}
		case "event-status":
			receiptSeen = a["attempt"] == "a" && a["consumer"] == "host" && a["instance"] == "instance" && a["status"] == "queued"
		}
		json.NewEncoder(w).Encode(v)
	}))
	defer server.Close()
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input, host := io.Pipe()
	output, service := io.Pipe()
	defer input.Close()
	defer host.Close()
	defer output.Close()
	defer service.Close()
	done := make(chan error, 1)
	go func() {
		done <- managedLoop(ctx, session.Descriptor{ID: "0123456789", ServiceVersion: 1, RunID: "run", HTTP: server.URL}, "host", r, input, service)
	}()
	dec := json.NewDecoder(output)
	var ready obj
	if err := dec.Decode(&ready); err != nil {
		t.Fatal(err)
	}
	if ready["type"] != "ready" {
		t.Fatal(ready)
	}
	var frame struct {
		Type     string                    `json:"type"`
		Delivery protocol.DeliveryEnvelope `json:"delivery"`
	}
	if err := dec.Decode(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type == "liveness" {
		if err := dec.Decode(&frame); err != nil {
			t.Fatal(err)
		}
	}
	if frame.Type != "delivery" || frame.Delivery.Message != "canonical Go message" {
		t.Fatal(frame)
	}
	if err := json.NewEncoder(host).Encode(managedInput{Type: "receipt", Delivery: frame.Delivery, Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		seen := receiptSeen
		mu.Unlock()
		if seen {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("receipt not sent")
		}
		time.Sleep(time.Millisecond)
	}
	host.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
