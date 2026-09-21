package broker

import (
	"agentdebugger/internal/session"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"sync"
	"testing"
	"time"
)

// A controllable RPC target exposes the dispatch-before-start cancellation race.
type CoordinationTarget struct {
	mu      sync.Mutex
	running bool
	starts  int
	begin   chan struct{}
	halt    chan struct{}
	once    sync.Once
}

func (t *CoordinationTarget) State(_ map[string]any, out *map[string]any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	*out = map[string]any{"State": map[string]any{"Pid": 1, "Running": t.running}}
	return nil
}
func (t *CoordinationTarget) Command(a map[string]any, out *map[string]any) error {
	if a["name"] == "halt" {
		t.once.Do(func() { close(t.halt) })
		return nil
	}
	<-t.begin
	t.mu.Lock()
	t.running = true
	t.starts++
	t.mu.Unlock()
	<-t.halt
	t.mu.Lock()
	t.running = false
	t.mu.Unlock()
	*out = map[string]any{"State": map[string]any{"Pid": 1, "Running": false}}
	return nil
}
func coordinationFixture(t *testing.T) (*broker, *CoordinationTarget) {
	t.Helper()
	target := &CoordinationTarget{begin: make(chan struct{}), halt: make(chan struct{})}
	server := rpc.NewServer()
	if err := server.RegisterName("RPCServer", target); err != nil {
		t.Fatal(err)
	}
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
			go server.ServeCodec(jsonrpc.NewServerCodec(c))
		}
	}()
	b := &broker{s: session.Descriptor{Dir: t.TempDir(), Binding: &session.Binding{ID: "pi", Name: "Pi", Revision: 1}}, owner: "agent", rpcAddr: ln.Addr().String(), done: make(chan struct{}), generation: 1}
	t.Cleanup(func() { b.once.Do(func() { close(b.done) }); ln.Close() })
	return b, target
}
func TestExecutionTaskCancellationFencesDelayedDispatch(t *testing.T) {
	b, target := coordinationFixture(t)
	call := func(a obj) (obj, error) {
		b.mu.Lock()
		a["generation"] = b.generation
		b.mu.Unlock()
		return b.action(a)
	}
	if _, err := call(obj{"action": "continue", "binding": "pi"}); err == nil {
		t.Fatal("read-only agent resumed without authorization")
	}
	if _, err := call(obj{"action": "task-authorize", "binding": "pi", "instruction": "self authorize"}); err == nil {
		t.Fatal("agent self-authorized")
	}
	result, err := call(obj{"action": "task-authorize", "actor": "human", "instruction": "Inspect the retry"})
	if err != nil {
		t.Fatal(err)
	}
	task := result["task"].(session.ExecutionTask)
	if _, err = call(obj{"action": "continue", "binding": "pi", "task": task.ID}); err != nil {
		t.Fatal(err)
	}
	// Cancel before RPC starts. Its eventual start must still be halted.
	if _, err = call(obj{"action": "task-cancel", "actor": "browser", "task": task.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = call(obj{"action": "next", "binding": "pi", "task": task.ID}); err == nil {
		t.Fatal("cancelled task resumed using a fresh generation")
	}
	if _, err = call(obj{"action": "next", "actor": "human"}); err == nil {
		t.Fatal("allowed overlapping human command before cancellation settled")
	}
	close(target.begin)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		settled := !b.moving && !b.interrupting
		b.mu.Unlock()
		if settled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.mu.Lock()
	settled := !b.moving && !b.interrupting
	b.mu.Unlock()
	if !settled {
		t.Fatal("cancelled dispatch did not settle")
	}
	target.mu.Lock()
	starts := target.starts
	target.mu.Unlock()
	if starts != 1 {
		t.Fatalf("executed %d commands", starts)
	}
	if _, err = call(obj{"action": "task-authorize", "actor": "human", "instruction": "New investigation"}); err != nil {
		t.Fatal(err)
	}
	if _, err = call(obj{"action": "continue", "binding": "pi", "task": task.ID}); err == nil {
		t.Fatal("old task accepted under a new grant")
	}
}
func TestTaskBindingExpiryCompletionAndDisconnect(t *testing.T) {
	b, _ := coordinationFixture(t)
	authorize := func() {
		t.Helper()
		if _, err := b.action(obj{"action": "task-authorize", "actor": "human", "generation": b.generation, "instruction": "Inspect"}); err != nil {
			t.Fatal(err)
		}
	}
	authorize()
	if err := b.taskValid(obj{"task": b.s.Task.ID, "binding": "other"}); err == nil {
		t.Fatal("foreign binding accepted")
	}
	b.s.Task.Expires = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	if err := b.taskValid(obj{"task": b.s.Task.ID, "binding": "pi"}); err == nil || b.s.Task.Status != "cancelled" {
		t.Fatal("expired grant accepted")
	}
	authorize()
	task := b.s.Task.ID
	if _, err := b.action(obj{"action": "task-complete", "generation": b.generation, "task": task, "binding": "pi"}); err != nil {
		t.Fatal(err)
	}
	if err := b.taskValid(obj{"task": task, "binding": "pi"}); err == nil {
		t.Fatal("completed grant accepted")
	}
	authorize()
	b.agentDisconnected = time.Now().Add(-10 * time.Second)
	go b.maintainTasks()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		b.mu.Lock()
		cancelled := b.s.Task.Status == "cancelled"
		b.mu.Unlock()
		if cancelled {
			return
		}
	}
	t.Fatal("disconnected agent retained grant")
}
