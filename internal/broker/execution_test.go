package broker

import (
	"agentdebugger/internal/backend"
	"agentdebugger/internal/dap"
	"agentdebugger/internal/session"
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

type delayedAdapter struct {
	mu       sync.Mutex
	conn     net.Conn
	requests chan obj
	paused   chan struct{}
}

func (a *delayedAdapter) send(v obj) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = dap.Write(a.conn, v)
}
func (a *delayedAdapter) reply(v obj, success bool) {
	a.send(obj{"type": "response", "request_seq": v["seq"], "command": v["command"], "success": success, "body": obj{}, "message": "delayed rejection"})
}
func (a *delayedAdapter) event(name string) {
	a.send(obj{"type": "event", "event": name, "body": obj{"threadId": 1, "reason": "breakpoint", "allThreadsStopped": true}})
}
func delayedFixture(t *testing.T, held ...string) (*broker, *delayedAdapter) {
	t.Helper()
	a := &delayedAdapter{requests: make(chan obj, 16), paused: make(chan struct{}, 16)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				prefix, err := r.Peek(1)
				if err != nil {
					return
				}
				if prefix[0] == '{' {
					decoder := json.NewDecoder(r)
					encoder := json.NewEncoder(c)
					for {
						var req obj
						if decoder.Decode(&req) != nil {
							return
						}
						result := obj{"State": obj{"Pid": 1, "Running": false, "currentGoroutine": obj{"id": 1}}}
						if req["method"] == "RPCServer.ListBreakpoints" {
							result = obj{"Breakpoints": []any{}}
						}
						if encoder.Encode(obj{"id": req["id"], "result": result, "error": nil}) != nil {
							return
						}
					}
				}
				a.mu.Lock()
				a.conn = c
				a.mu.Unlock()
				for {
					req, err := dap.Read(r)
					if err != nil {
						return
					}
					hold := false
					for _, command := range held {
						if req["command"] == command {
							hold = true
						}
					}
					if hold {
						a.requests <- req
						continue
					}
					switch req["command"] {
					case "continue", "next", "stepIn", "stepOut":
						a.requests <- req
					case "pause":
						a.reply(req, true)
						a.event("stopped")
						select {
						case a.paused <- struct{}{}:
						default:
						}
					default:
						a.reply(req, true)
					}
				}
			}()
		}
	}()
	d, err := backend.Open(ln.Addr().String(), nil, nil)
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	b := &broker{backend: d, s: session.Descriptor{ID: "s", RunID: "r", ServiceVersion: 1, Dir: t.TempDir()}, generation: 1, changed: make(chan struct{}), done: make(chan struct{})}
	b.watchBackend(d)
	t.Cleanup(func() { b.once.Do(func() { close(b.done) }); d.Close(); ln.Close() })
	return b, a
}
func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		if predicate() {
			return
		}
	}
	t.Fatal("state did not settle")
}
func executionRequest(t *testing.T, a *delayedAdapter) obj {
	t.Helper()
	select {
	case req := <-a.requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("request was not dispatched")
		return nil
	}
}
func beginTestExecution(t *testing.T, b *broker, verb string) {
	t.Helper()
	b.mu.Lock()
	err := b.beginExecution(verb, obj{"currentGoroutine": obj{"id": 1}})
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPauseDoesNotWaitForExecutionAcknowledgement(t *testing.T) {
	b, a := delayedFixture(t)
	dispatched := make(chan struct{})
	go func() { beginTestExecution(t, b, "continue"); close(dispatched) }()
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("dispatch blocked on acknowledgement")
	}
	request := executionRequest(t, a)
	a.event("continued")
	eventually(t, func() bool { return truth(b.backend.State()["Running"]) })
	result, err := b.action(obj{"action": "pause", "actor": "human", "run": "r", "commandId": "pause", "generation": 0})
	if err != nil || result["status"] != "pause requested" {
		t.Fatalf("%v %v", result, err)
	}
	select {
	case <-a.paused:
	case <-time.After(time.Second):
		t.Fatal("pause blocked behind continue response")
	}
	a.reply(request, true)
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.moving && !b.interrupting && !b.pendingFor(b.backend)
	})
}

func TestOldRejectedResponseCannotClearNewExecution(t *testing.T) {
	b, a := delayedFixture(t)
	beginTestExecution(t, b, "continue")
	old := executionRequest(t, a)
	a.event("stopped")
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return !b.moving })
	beginTestExecution(t, b, "next")
	next := executionRequest(t, a)
	a.event("continued")
	a.reply(old, false)
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); _, pending := b.pendingExecution[1]; return !pending })
	b.mu.Lock()
	moving, errorText := b.moving, b.lastError
	b.mu.Unlock()
	if !moving || errorText != "" {
		t.Fatalf("old response corrupted current execution: moving=%v error=%s", moving, errorText)
	}
	a.reply(next, true)
	a.event("stopped")
}

func TestExecutionCommandIDCannotBeRedispatched(t *testing.T) {
	b, a := delayedFixture(t)
	_, err := b.action(obj{"action": "continue", "actor": "human", "run": "r", "commandId": "once", "generation": 1})
	if err != nil {
		t.Fatal(err)
	}
	first := executionRequest(t, a)
	a.reply(first, true)
	a.event("stopped")
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return !b.moving })
	b.mu.Lock()
	generation := b.generation
	b.mu.Unlock()
	_, err = b.action(obj{"action": "continue", "actor": "human", "run": "r", "commandId": "once", "generation": generation})
	if err == nil {
		t.Fatal("duplicate command accepted")
	}
	select {
	case <-a.requests:
		t.Fatal("duplicate reached backend")
	default:
	}
}

func TestPauseStillWorksAtCommandHistoryLimit(t *testing.T) {
	b, _ := delayedFixture(t)
	b.mu.Lock()
	b.seenCommands = map[string]bool{}
	for i := 0; i < 4096; i++ {
		b.seenCommands[fmt.Sprint(i)] = true
	}
	b.mu.Unlock()
	if _, err := b.action(obj{"action": "pause", "actor": "human", "run": "r", "generation": 0}); err != nil {
		t.Fatalf("history cap blocked human pause: %v", err)
	}
}

func TestExpiredServiceTaskCannotDispatchOrRenew(t *testing.T) {
	b, a := delayedFixture(t)
	binding := &session.Binding{ID: "agent", Revision: 1, Name: "Agent"}
	b.s.Binding = binding
	b.s.Task = &session.ExecutionTask{ID: "task", Binding: binding, Status: "active", Expires: time.Now().Add(-time.Second).Format(time.RFC3339Nano)}
	for _, verb := range []string{"continue", "task-heartbeat"} {
		_, err := b.action(obj{"action": verb, "actor": "agent", "run": "r", "binding": "agent", "task": "task", "commandId": "expired", "generation": b.generation})
		if err == nil {
			t.Fatalf("expired task accepted %s", verb)
		}
	}
	if b.s.Task.Status != "cancelled" {
		t.Fatal("scope not cancelled")
	}
	select {
	case <-a.requests:
		t.Fatal("expired scope reached adapter")
	default:
	}
}

func TestHumanStepInterruptsAgentBeforeDelayedReply(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			b, a := delayedFixture(t)
			binding := &session.Binding{ID: "agent", Revision: 1, Name: "Agent"}
			b.s.Binding = binding
			b.s.Task = &session.ExecutionTask{ID: "task", Binding: binding, Status: "active", Expires: time.Now().Add(time.Minute).Format(time.RFC3339Nano)}
			beginTestExecution(t, b, "continue")
			old := executionRequest(t, a)
			b.mu.Lock()
			generation := b.generation
			b.mu.Unlock()
			if stale {
				generation--
			}
			_, err := b.action(obj{"action": "next", "actor": "human", "run": "r", "generation": generation, "commandId": "human"})
			if err == nil {
				t.Fatal("stepped during pending agent execution")
			}
			select {
			case <-a.paused:
			case <-time.After(time.Second):
				t.Fatal("human step did not interrupt agent")
			}
			b.mu.Lock()
			status := b.s.Task.Status
			generation = b.generation
			b.mu.Unlock()
			if status != "cancelled" {
				t.Fatal("agent retained scope")
			}
			if _, err := b.action(obj{"action": "continue", "actor": "agent", "run": "r", "generation": generation, "commandId": "late", "binding": "agent", "task": "task"}); err == nil {
				t.Fatal("cancelled agent resumed")
			}
			select {
			case <-a.requests:
				t.Fatal("queued movement reached adapter")
			default:
			}
			a.reply(old, true)
			eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return !b.moving && !b.interrupting })
		})
	}
}

func TestEditorStepCancelsPendingAgentDispatch(t *testing.T) {
	b, a := delayedFixture(t)
	binding := &session.Binding{ID: "agent", Revision: 1, Name: "Agent"}
	b.s.Binding = binding
	b.s.Task = &session.ExecutionTask{ID: "task", Binding: binding, Status: "active", Expires: time.Now().Add(time.Minute).Format(time.RFC3339Nano)}
	beginTestExecution(t, b, "continue")
	old := executionRequest(t, a)
	server, editor := net.Pipe()
	defer editor.Close()
	go b.connectSharedDAP(server)
	_ = editor.SetDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(editor)
	replies := make(chan obj, 32)
	go func() {
		for {
			v, e := dap.Read(r)
			if e != nil {
				close(replies)
				return
			}
			replies <- v
		}
	}()
	for seq, command := range []string{"initialize", "attach", "configurationDone"} {
		if err := dap.Write(editor, obj{"seq": seq + 1, "type": "request", "command": command}); err != nil {
			t.Fatal(err)
		}
		for {
			reply, ok := <-replies
			if !ok {
				t.Fatal("DAP closed")
			}
			if num(reply["request_seq"]) == seq+1 {
				break
			}
		}
	}
	if err := dap.Write(editor, obj{"seq": 4, "type": "request", "command": "next", "arguments": obj{"threadId": 1}}); err != nil {
		t.Fatal(err)
	}
	for {
		result, ok := <-replies
		if !ok {
			t.Fatal("DAP closed")
		}
		if result["type"] == "response" {
			if result["success"] != false {
				t.Fatal("editor step raced agent dispatch")
			}
			break
		}
	}
	b.mu.Lock()
	status := b.s.Task.Status
	b.mu.Unlock()
	if status != "cancelled" {
		t.Fatal("editor did not cancel scope")
	}
	a.reply(old, true)
	// Drain events while waiting so the fake editor cannot block the broadcaster.
	editor.Close()
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return !b.moving && !b.interrupting })
}
