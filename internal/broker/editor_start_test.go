package broker

import (
	"agentdebugger/internal/dap"
	"bufio"
	"net"
	"testing"
	"time"
)

func TestLaunchWaitsForConfiguration(t *testing.T) {
	b, a := delayedFixture(t, "continue")
	front, client := net.Pipe()
	defer client.Close()
	go b.connectSharedDAP(front)
	replies := make(chan obj, 32)
	go func() {
		r := bufio.NewReader(client)
		for {
			v, e := dap.Read(r)
			if e != nil {
				return
			}
			replies <- v
		}
	}()
	seq := 0
	request := func(command string, args obj) obj {
		t.Helper()
		seq++
		if e := dap.Write(client, obj{"seq": seq, "type": "request", "command": command, "arguments": args}); e != nil {
			t.Fatal(e)
		}
		for {
			select {
			case r := <-replies:
				if num(r["request_seq"]) == seq {
					return r
				}
			case <-time.After(3 * time.Second):
				t.Fatal("DAP timeout")
			}
		}
	}
	if !truth(request("initialize", obj{})["success"]) || !truth(request("launch", obj{})["success"]) {
		t.Fatal("launch failed")
	}
	if truth(request("continue", obj{})["success"]) {
		t.Fatal("execution before configuration")
	}
	select {
	case r := <-a.requests:
		t.Fatalf("unexpected early movement %v", r)
	default:
	}
	if !truth(request("configurationDone", obj{})["success"]) {
		t.Fatal("configuration failed")
	}
	select {
	case r := <-a.requests:
		if r["command"] != "continue" {
			t.Fatal(r)
		}
		a.reply(r, true)
	case <-time.After(time.Second):
		t.Fatal("launch never continued")
	}
	if truth(request("configurationDone", obj{})["success"]) {
		t.Fatal("duplicate configuration accepted")
	}
	request("disconnect", obj{})
}

func TestAbandonedEditorStartupEndsTarget(t *testing.T) {
	b, _ := delayedFixture(t)
	b.awaitEditorConfiguration(time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		stopped := b.s.Stopped
		b.mu.Unlock()
		if stopped {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("abandoned startup left target live")
}
func TestCompletedEditorStartupSurvivesLease(t *testing.T) {
	b, _ := delayedFixture(t)
	b.editorConfigured = true
	b.awaitEditorConfiguration(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.s.Stopped {
		t.Fatal("configured session ended")
	}
}

func TestEditorStartupLeaseRejectsExternalTarget(t *testing.T) {
	for _, options := range []Options{{Service: true, Backend: "dap", EditorStartup: true, AttachPID: 123}, {Service: true, Backend: "dap", EditorStartup: true, Recover: true}, {EditorStartup: true}} {
		if err := Serve(options); err == nil {
			t.Fatal("invalid editor ownership accepted")
		}
	}
}
