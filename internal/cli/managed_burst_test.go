package cli

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegressionManagedBurstDoesNotDeadlock(t *testing.T) {
	t.Setenv("DEBUG_HANDOVER_HOME", t.TempDir())
	var claims, receipts atomic.Int32
	r := protocol.Recipient{Kind: "agent", ID: "pi", Revision: 1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "GET" {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, ": connected\n\n")
			w.(http.Flusher).Flush()
			<-req.Context().Done()
			return
		}
		var a obj
		json.NewDecoder(req.Body).Decode(&a)
		v := obj{}
		switch a["action"] {
		case "consumer-open":
			v["consumer"] = session.Consumer{ID: "host", Instance: "instance", Recipient: r}
		case "consumer-challenge":
			v["challenge"] = "proof"
		case "consumer-next":
			n := claims.Add(1)
			if n <= 12 {
				v["delivery"] = protocol.DeliveryEnvelope{Version: 1, Session: "0123456789", Kind: "question", Thread: fmt.Sprint(n), Subject: fmt.Sprint(n), Attempt: fmt.Sprint(n), Recipient: r, Status: "sending", Message: "question"}
			}
		case "event-status":
			receipts.Add(1)
		}
		json.NewEncoder(w).Encode(v)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input, replies := io.Pipe()
	output, service := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- managedLoop(ctx, session.Descriptor{ID: "0123456789", ServiceVersion: 1, RunID: "run", HTTP: srv.URL}, "host", r, input, service)
	}()
	hostDone := make(chan struct{})
	go func() {
		defer close(hostDone)
		dec, enc := json.NewDecoder(output), json.NewEncoder(replies)
		for {
			var f struct {
				Type     string                    `json:"type"`
				Delivery protocol.DeliveryEnvelope `json:"delivery"`
			}
			if dec.Decode(&f) != nil {
				return
			}
			if f.Type == "delivery" {
				if enc.Encode(managedInput{Type: "receipt", Delivery: f.Delivery, Status: "queued"}) != nil {
					return
				}
			}
		}
	}()
	deadline := time.Now().Add(time.Second)
	for receipts.Load() < 12 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got, claimed := receipts.Load(), claims.Load()
	cancel()
	input.Close()
	replies.Close()
	output.Close()
	service.Close()
	<-done
	<-hostDone
	if got != 12 {
		t.Fatalf("burst stalled: %d claims, %d persisted receipts out of 12", claimed, got)
	}
}
