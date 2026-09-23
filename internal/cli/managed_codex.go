package cli

import (
	"agentdebugger/internal/agents/codex"
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// The Codex host layer only invokes its queue and reports the outcome. Managed
// Go delivery owns subjects, attempts, replay and receipt transitions.
func managedCodex(s session.Descriptor, cfg bridgeConfig) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	hostInput, hostReplies := io.Pipe()
	hostOutput, serviceOutput := io.Pipe()
	defer hostInput.Close()
	defer hostReplies.Close()
	defer hostOutput.Close()
	defer serviceOutput.Close()
	go func() {
		defer cancel()
		defer hostReplies.Close()
		decoder, encoder := json.NewDecoder(hostOutput), json.NewEncoder(hostReplies)
		for {
			var frame struct {
				Type     string                    `json:"type"`
				Delivery protocol.DeliveryEnvelope `json:"delivery"`
			}
			if decoder.Decode(&frame) != nil {
				return
			}
			if frame.Type != "delivery" {
				continue
			}
			sendCtx, stop := context.WithTimeout(ctx, 15*time.Second)
			out, err := codex.Queue(sendCtx, cfg.Executable, cfg.Thread, frame.Delivery.Message)
			stop()
			status, detail := "queued", ""
			if err != nil {
				status = "unknown"
				detail = string(out)
				if detail == "" {
					detail = err.Error()
				}
			}
			if encoder.Encode(managedInput{Type: "receipt", Delivery: frame.Delivery, Status: status, Error: detail}) != nil {
				return
			}
		}
	}()
	return managedLoop(ctx, s, "codex:"+cfg.Thread, protocol.Recipient{Kind: "agent", ID: cfg.Binding.ID, Revision: cfg.Binding.Revision, Name: cfg.Binding.Name}, hostInput, serviceOutput)
}
