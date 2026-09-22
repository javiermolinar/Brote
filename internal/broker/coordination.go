package broker

import (
	"agentdebugger/internal/delve"
	"agentdebugger/internal/session"
	"fmt"
	"strings"
	"time"
)

const taskLease = 60 * time.Second

func executionAction(verb string) bool {
	switch verb {
	case "continue", "next", "step", "stepout", "pause", "stop":
		return true
	}
	return false
}
func human(actor string) bool {
	return actor == "browser" || actor == "human" || actor == "vscode" || actor == "zed"
}
func (b *broker) activeTask() bool {
	return b.s.Task != nil && (b.s.Task.Status == "authorized" || b.s.Task.Status == "active")
}
func (b *broker) taskMatches(a obj) bool {
	t := b.s.Task
	return t != nil && t.ID == str(a["task"]) && t.Binding != nil && b.s.Binding != nil && *t.Binding == *b.s.Binding && str(a["binding"]) == t.Binding.ID
}
func (b *broker) taskValid(a obj) error {
	t := b.s.Task
	if !b.activeTask() || !b.taskMatches(a) {
		return fmt.Errorf("agent execution requires a current debugging task; record the user's request with task-start")
	}
	deadline, err := time.Parse(time.RFC3339Nano, t.Expires)
	if err != nil || !time.Now().Before(deadline) {
		b.cancelTask("lease expired")
		_ = b.interruptExecution()
		return fmt.Errorf("execution task expired")
	}
	return nil
}
func (b *broker) cancelTask(reason string) {
	if !b.activeTask() {
		return
	}
	b.s.Task.Status = "cancelled"
	b.s.Task.Reason = reason
	b.record("task.cancelled", "core", b.s.Task)
	_ = b.emit("task.cancelled", b.s.Task.ID)
}
func (b *broker) coordinate(a obj) (obj, error) {
	verb := str(a["action"])
	switch verb {
	case "task-start", "task-authorize":
		actor := str(a["actor"])
		fromAgent := verb == "task-start"
		if fromAgent {
			if actor != "agent" || b.s.Binding == nil || str(a["binding"]) != b.s.Binding.ID || uint64(num(a["revision"])) != b.s.Binding.Revision {
				return nil, fmt.Errorf("task-start requires the current agent binding and revision")
			}
		} else if !human(actor) {
			return nil, fmt.Errorf("use task-start to record the user's debugging request")
		}
		instruction := strings.TrimSpace(str(a["instruction"]))
		if instruction == "" || len(instruction) > 4096 {
			return nil, fmt.Errorf("provide an investigation instruction (1–4096 bytes)")
		}
		if b.activeTask() {
			return nil, fmt.Errorf("finish or cancel the current task first")
		}
		state, err := b.state()
		if err != nil {
			return nil, err
		}
		if stateStatus(state, b.moving) != "paused" || truth(state["NextInProgress"]) {
			return nil, fmt.Errorf("start a debugging task at a settled pause")
		}
		if b.s.Binding == nil {
			return nil, fmt.Errorf("bind an agent first")
		}
		b.s.Task = &session.ExecutionTask{ID: session.NewID(16), Instruction: instruction, Status: "authorized", Delivery: "pending", Binding: copyBinding(b.s.Binding), Expires: time.Now().Add(taskLease).UTC().Format(time.RFC3339Nano)}
		kind := "task.authorized"
		if fromAgent {
			// The request is already in this conversation. Do not deliver it back
			// through the event bridge as a duplicate follow-up turn.
			b.s.Task.Delivery = "acknowledged"
			kind = "task.started"
		}
		b.generation++
		if err := b.emit(kind, b.s.Task.ID); err != nil {
			b.s.Task = nil
			return nil, err
		}
		b.record(kind, actor, b.s.Task)
	case "task-delivery":
		if err := b.taskValid(a); err != nil {
			return nil, err
		}
		if uint64(num(a["revision"])) != b.s.Binding.Revision {
			return nil, fmt.Errorf("task binding revision changed")
		}
		t := b.s.Task
		status := str(a["status"])
		if t.Delivery == "acknowledged" && (status == "queued" || status == "unknown" || status == "failed") {
			return obj{"task": b.taskView()}, nil
		}
		allowed := (status == "sending" && t.Delivery == "pending") || ((status == "queued" || status == "failed" || status == "unknown") && t.Delivery == "sending")
		if !allowed {
			return nil, fmt.Errorf("invalid task delivery transition %s -> %s", t.Delivery, status)
		}
		previous, previousError := t.Delivery, t.DeliveryError
		t.Delivery, t.DeliveryError = status, str(a["error"])
		if len(t.DeliveryError) > 1024 {
			t.DeliveryError = t.DeliveryError[:1024]
		}
		if err := b.persist(); err != nil {
			t.Delivery, t.DeliveryError = previous, previousError
			return nil, err
		}
	case "task-cancel":
		if id := str(a["task"]); id != "" && (b.s.Task == nil || b.s.Task.ID != id) {
			return nil, fmt.Errorf("execution task changed")
		}
		if !human(str(a["actor"])) {
			if err := b.taskValid(a); err != nil {
				return nil, err
			}
		}
		b.cancelTask("cancelled by " + str(a["actor"]))
		b.generation++
		if err := b.interruptExecution(); err != nil {
			return nil, err
		}
	case "task-heartbeat", "task-complete":
		// Target exit completes its task automatically. Closing that same scope
		// again is harmless, including when the caller missed the exit event.
		if verb == "task-complete" && b.taskMatches(a) && b.s.Task.Status == "completed" {
			return obj{"task": b.taskView()}, nil
		}
		if err := b.taskValid(a); err != nil {
			return nil, err
		}
		if verb == "task-complete" {
			state, err := b.state()
			if err != nil {
				return nil, err
			}
			if stateStatus(state, b.moving) == "running" || truth(state["NextInProgress"]) || b.interrupting {
				return nil, fmt.Errorf("settle execution before completing the task")
			}
			b.s.Task.Status = "completed"
			b.record("task.completed", "agent", b.s.Task)
			if err := b.emit("task.completed", b.s.Task.ID); err != nil {
				return nil, err
			}
		} else {
			b.s.Task.Delivery = "acknowledged"
			b.s.Task.DeliveryError = ""
			b.s.Task.Expires = time.Now().Add(taskLease).UTC().Format(time.RFC3339Nano)
			if err := b.persist(); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unknown task action")
	}
	return obj{"task": b.taskView()}, nil
}

// Dispatch happens while holding b.mu, before a human can cancel the grant.
// DAP acknowledges dispatch immediately. RPC Begin writes its request synchronously
// and returns a waiter; cancellation fences new execution until that waiter settles.
func (b *broker) beginExecution(verb string, state obj) error {
	if b.moving || b.interrupting {
		return fmt.Errorf("execution already in progress")
	}
	b.moving = true
	b.handleEpoch++
	b.generation++
	b.lastError = ""
	if b.backend != nil {
		command := map[string]string{"continue": "continue", "next": "next", "step": "stepIn", "stepout": "stepOut"}[verb]
		_, err := b.backend.Request(command, obj{"threadId": asObj(state["currentGoroutine"])["id"]})
		if err != nil {
			b.moving = false
			b.lastError = err.Error()
		}
		return err
	}
	name := verb
	if name == "stepout" {
		name = "stepOut"
	}
	wait, err := delve.Begin(b.rpcAddr, "Command", obj{"name": name})
	if err != nil {
		b.moving = false
		return err
	}
	go func() {
		_, err := wait()
		b.mu.Lock()
		defer b.mu.Unlock()
		b.moving = false
		b.generation++
		kind := "stopped"
		if current, e := b.state(); e == nil && truth(current["exited"]) {
			kind = "target_exited"
		}
		if _, exited := delve.ExitState(err); err != nil && !exited {
			b.lastError = err.Error()
			b.record("execution.failed", "debugger", obj{"error": err.Error()})
		}
		_ = b.emit(kind, "")
	}()
	return nil
}
func (b *broker) interruptExecution() error {
	if b.interrupting {
		return nil
	}
	state, err := b.state()
	if err != nil {
		return err
	}
	if truth(state["Running"]) {
		_, err = b.rpc("Command", obj{"name": "halt"})
		return err
	}
	if !b.moving {
		return nil
	}
	// RPC may have been written but not started. Never release the execution fence
	// until the pending command finishes or can be halted, so a late start is safe.
	b.interrupting = true
	go func() {
		defer func() { b.mu.Lock(); b.interrupting = false; b.mu.Unlock() }()
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-b.done:
				return
			case <-tick.C:
			}
			b.mu.Lock()
			state, err := b.state()
			if err != nil {
				b.lastError = err.Error()
				b.mu.Unlock()
				continue
			}
			if !b.moving || truth(state["exited"]) {
				b.mu.Unlock()
				return
			}
			if truth(state["Running"]) {
				_, err = b.rpc("Command", obj{"name": "halt"})
				if err != nil {
					b.lastError = err.Error()
				}
				b.mu.Unlock()
				if err == nil {
					return
				}
				continue
			}
			b.mu.Unlock()
		}
	}()
	return nil
}
func (b *broker) maintainTasks() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-tick.C:
		}
		b.mu.Lock()
		if b.activeTask() {
			deadline, _ := time.Parse(time.RFC3339Nano, b.s.Task.Expires)
			disconnected := b.agentStreams == 0 && !b.agentDisconnected.IsZero() && time.Since(b.agentDisconnected) > 5*time.Second
			if !time.Now().Before(deadline) || disconnected {
				reason := "lease expired"
				if disconnected {
					reason = "agent disconnected"
				}
				b.cancelTask(reason)
				if err := b.interruptExecution(); err != nil {
					b.lastError = err.Error()
				}
			}
		}
		b.mu.Unlock()
	}
}

func (b *broker) taskView() any {
	if b.s.Task == nil {
		return nil
	}
	task := *b.s.Task
	task.Binding = copyBinding(task.Binding)
	return task
}
