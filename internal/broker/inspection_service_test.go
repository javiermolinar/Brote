package broker

import (
	"agentdebugger/internal/dap"
	"bufio"
	"net"
	"testing"
	"time"
)

func TestEditorPauseWhileInspectionWaits(t *testing.T) {
	b, a := delayedFixture(t, "stackTrace")
	server, editor := net.Pipe()
	finished := make(chan struct{})
	go func() { defer close(finished); b.connectSharedDAP(server) }()
	defer func() {
		_ = editor.Close()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("editor handler did not finish cleanup")
		}
	}()
	_ = editor.SetDeadline(time.Now().Add(3 * time.Second))
	if err := dap.Write(editor, obj{"seq": 1, "type": "request", "command": "stackTrace", "arguments": obj{"threadId": 1, "levels": 30}}); err != nil {
		t.Fatal(err)
	}
	// Drain the editor concurrently: backend events are delivered to it too.
	responses := make(chan obj, 8)
	go func() {
		r := bufio.NewReader(editor)
		for {
			v, e := dap.Read(r)
			if e != nil {
				return
			}
			responses <- v
		}
	}()
	request := executionRequest(t, a)
	a.event("continued")
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.moving })
	if err := dap.Write(editor, obj{"seq": 2, "type": "request", "command": "pause"}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-a.paused:
	case <-time.After(time.Second):
		t.Fatal("inspection blocked Pause")
	}
	a.reply(request, true)
	for {
		select {
		case v := <-responses:
			if num(v["request_seq"]) == 1 {
				if truth(v["success"]) {
					t.Fatal("obsolete stack accepted")
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("no inspection result")
		}
	}
}
func TestSnapshotRejectsMovementDuringInspection(t *testing.T) {
	b, a := delayedFixture(t, "stackTrace")
	result := make(chan error, 1)
	go func() { _, err := b.snapshot(1, 0, false); result <- err }()
	request := executionRequest(t, a)
	a.event("continued")
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.moving })
	b.mu.Lock()
	generation := b.generation
	b.mu.Unlock()
	if _, err := b.action(obj{"action": "pause", "actor": "human", "run": "r", "generation": generation}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.paused:
	case <-time.After(time.Second):
		t.Fatal("snapshot blocked pause")
	}
	a.reply(request, true)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("mixed-state snapshot accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot exceeded deadline")
	}
}
func TestEndedServiceRejectsRestart(t *testing.T) {
	b, _ := delayedFixture(t)
	b.mu.Lock()
	b.s.Stopped = true
	b.mu.Unlock()
	if _, err := b.action(obj{"action": "restart", "actor": "human", "run": "r", "generation": 1}); err == nil {
		t.Fatal("ended session restarted")
	}
}

func TestServiceSnapshotSelectsDeepFramePage(t *testing.T) {
	b, a := delayedFixture(t, "stackTrace")
	result := make(chan obj, 1)
	failed := make(chan error, 1)
	go func() {
		v, err := b.snapshot(1, 35, false)
		if err != nil {
			failed <- err
		} else {
			result <- v
		}
	}()
	first := executionRequest(t, a)
	if num(asObj(first["arguments"])["startFrame"]) != 30 {
		t.Fatal("deep stack page not requested")
	}
	frames := []any{}
	for i := 0; i < 6; i++ {
		frames = append(frames, obj{"id": i + 1, "name": "frame", "line": i + 30, "source": obj{"path": "/missing.go"}})
	}
	a.send(obj{"type": "response", "request_seq": first["seq"], "command": "stackTrace", "success": true, "body": obj{"stackFrames": frames}})
	selected := executionRequest(t, a)
	if num(asObj(selected["arguments"])["startFrame"]) != 35 {
		t.Fatal("locals came from the wrong absolute frame")
	}
	replyCaptureStack(a, selected)
	select {
	case v := <-result:
		if num(v["frameOffset"]) != 30 || num(v["frame"]) != 5 {
			t.Fatal("selected frame page lost")
		}
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("snapshot stalled")
	}
}
