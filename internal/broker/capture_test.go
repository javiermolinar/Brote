package broker

import (
	"agentdebugger/internal/protocol"
	"testing"
	"time"
)

func primeCapture(b *broker, limit int) {
	b.currentStop = stopAttribution{Eligible: true, Goroutine: 1, Definitions: []protocol.Definition{{ID: "capture", Kind: "tracepoint", Enabled: true, CaptureLimit: limit}}}
}
func TestCaptureInvalidatedByMovement(t *testing.T) {
	b, a := delayedFixture(t, "stackTrace")
	b.mu.Lock()
	primeCapture(b, 1)
	b.captureStop()
	b.mu.Unlock()
	request := executionRequest(t, a)
	b.mu.Lock()
	b.handleEpoch++
	b.mu.Unlock()
	a.reply(request, true)
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.captures) == 1 })
	b.mu.Lock()
	defer b.mu.Unlock()
	record := b.captures[0]
	if record.Status != "failed" || record.Error.Code != "stale_capture" || record.Snapshot != nil {
		t.Fatalf("obsolete evidence retained: %#v", record)
	}
}
func TestCaptureDeadlineAndExhaustion(t *testing.T) {
	b, a := delayedFixture(t, "stackTrace")
	b.mu.Lock()
	primeCapture(b, 1)
	b.captureStop()
	b.mu.Unlock()
	_ = executionRequest(t, a)
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		b.mu.Lock()
		done := len(b.captures) == 1
		b.mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.mu.Lock()
	if len(b.captures) != 1 || b.captures[0].Status != "failed" {
		b.mu.Unlock()
		t.Fatal("capture did not fail at deadline")
	}
	b.captureStop()
	b.mu.Unlock()
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.captures) == 2 })
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.captures[1].Status != "skipped" || b.s.CaptureCounts["capture"] != 1 || b.captures[1].Sequence != 2 {
		t.Fatal("capture limit reset or sequence lost")
	}
	if b.moving {
		t.Fatal("failed capture resumed target")
	}
}
func TestCaptureRingBounded(t *testing.T) {
	b, _ := delayedFixture(t)
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := 0; i < 100; i++ {
		b.appendCapture(captureRecord{CaptureOutcome: protocol.CaptureOutcome{Sequence: uint64(i), Status: "skipped"}})
	}
	if len(b.captures) != 64 || b.captures[0].Sequence != 36 {
		t.Fatal("capture history not bounded")
	}
}

func replyCaptureStack(a *delayedAdapter, request obj) {
	a.send(obj{"type": "response", "request_seq": request["seq"], "command": request["command"], "success": true, "body": obj{"stackFrames": []any{obj{"id": 1, "name": "main.work", "line": 5, "source": obj{"path": "/missing/main.go"}}}}})
}
func TestCaptureResumeFences(t *testing.T) {
	for _, change := range []string{"pause", "step", "configuration", "end", "failed"} {
		t.Run(change, func(t *testing.T) {
			b, a := delayedFixture(t, "stackTrace")
			b.mu.Lock()
			primeCapture(b, 2)
			b.setExecutionIntent("continue", "human")
			b.captureStop()
			b.mu.Unlock()
			request := executionRequest(t, a)
			b.mu.Lock()
			switch change {
			case "pause":
				_ = b.humanIntent("pause")
			case "step":
				_ = b.humanIntent("next")
			case "configuration":
				b.s.Definitions.Revision++
			case "end":
				b.closing = true
			}
			b.mu.Unlock()
			if change == "failed" {
				a.reply(request, false)
			} else {
				replyCaptureStack(a, request)
				replyCaptureStack(a, executionRequest(t, a))
			}
			eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.captures) == 1 })
			select {
			case request := <-a.requests:
				t.Fatalf("capture dispatched obsolete execution: %v", request)
			default:
			}
		})
	}
}
func TestBackToBackCaptureBeforeContinueReply(t *testing.T) {
	b, a := delayedFixture(t, "stackTrace")
	b.mu.Lock()
	primeCapture(b, 3)
	b.s.Definitions.Items = b.currentStop.Definitions
	b.resolutions = []definitionResolution{{DefinitionID: "capture", AdapterID: 7, Verified: true, Run: "r"}}
	b.setExecutionIntent("continue", "human")
	b.captureStop()
	b.mu.Unlock()
	replyCaptureStack(a, executionRequest(t, a))
	replyCaptureStack(a, executionRequest(t, a))
	first := executionRequest(t, a)
	if first["command"] != "continue" {
		t.Fatal("successful capture did not continue")
	}
	a.send(obj{"type": "event", "event": "stopped", "body": obj{"reason": "breakpoint", "threadId": 1, "allThreadsStopped": true, "hitBreakpointIds": []any{7}}})
	replyCaptureStack(a, executionRequest(t, a))
	replyCaptureStack(a, executionRequest(t, a))
	second := executionRequest(t, a)
	if second["command"] != "continue" {
		t.Fatal("next hit waited on earlier continue reply")
	}
	b.mu.Lock()
	count := len(b.captures)
	hits := b.s.CaptureCounts["capture"]
	b.mu.Unlock()
	if count != 2 || hits != 2 {
		t.Fatalf("duplicate or dropped hit: %d/%d", count, hits)
	}
	a.reply(first, false)
	b.mu.Lock()
	moving := b.moving
	b.mu.Unlock()
	if !moving {
		t.Fatal("late rejection cleared newer continuation")
	}
	a.reply(second, true)
}
