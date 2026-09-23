package broker

import (
	"agentdebugger/internal/backend"
	"agentdebugger/internal/dap"
	"errors"
	"fmt"
	"time"
)

// dispatchDAP sends under b.mu, then waits without it. A response can arrive
// after a stop or after another dispatch; only its own unchanged epoch can
// roll back movement on rejection.
func (b *broker) dispatchDAP(command string, args obj, reply func(obj, error)) error {
	if b.moving || b.interrupting || truth(b.backend.State()["Running"]) {
		return fmt.Errorf("execution already in progress")
	}
	b.moving = true
	b.handleEpoch++
	b.generation++
	b.lastError = ""
	b.dispatchSequence++
	id, epoch, source := b.dispatchSequence, b.handleEpoch, b.backend
	wait, err := source.Begin(command, args)
	if err != nil {
		b.moving = false
		b.lastError = err.Error()
		return err
	}
	if b.pendingExecution == nil {
		b.pendingExecution = map[uint64]*backend.Delve{}
	}
	b.pendingExecution[id] = source
	b.record("execution.dispatched", "core", obj{"command": command, "dispatchId": id, "generation": b.generation})
	go func() {
		body, err := wait()
		b.mu.Lock()
		delete(b.pendingExecution, id)
		current := b.backend == source
		if current && err != nil {
			var rejected *dap.RejectedError
			definite := errors.As(err, &rejected)
			if b.handleEpoch == epoch {
				b.lastError = err.Error()
				if definite {
					b.moving = false
				} else {
					_ = b.interruptDAP()
				}
			} else if definite && b.dispatchSequence == id && !truth(source.State()["Running"]) {
				b.moving = false
			}
		}
		if current {
			outcome := "acknowledged"
			if err != nil {
				outcome = "failed"
			}
			b.trace.Action(command, outcome)
			b.record("execution.acknowledged", "debugger", obj{"dispatchId": id, "error": errorString(err)})
		}
		b.mu.Unlock()
		if reply != nil {
			reply(body, err)
		}
	}()
	return nil
}

func (b *broker) pendingFor(source *backend.Delve) bool {
	for _, owner := range b.pendingExecution {
		if owner == source {
			return true
		}
	}
	return false
}

// Keep the interruption fence until the old dispatch and its stop have both
// settled. The worker does not hold b.mu while waiting for Delve replies.
func (b *broker) interruptDAP() error {
	source := b.backend
	if !b.moving && !truth(source.State()["Running"]) && !b.pendingFor(source) {
		return nil
	}
	b.interrupting = true
	b.handleEpoch++
	go func() {
		deadline := time.Now().Add(30 * time.Second)
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		for {
			b.mu.Lock()
			if b.backend != source {
				b.mu.Unlock()
				return
			}
			if time.Now().After(deadline) {
				b.lastError = "pause could not be confirmed; execution remains fenced; inspect or restart the session"
				b.mu.Unlock()
				return
			}
			if !b.moving && !truth(source.State()["Running"]) && !b.pendingFor(source) {
				b.interrupting = false
				b.mu.Unlock()
				return
			}
			b.mu.Unlock()
			_, err := source.Request("pause", obj{"threadId": asObj(source.State()["currentGoroutine"])["id"]})
			b.mu.Lock()
			if b.backend != source {
				b.mu.Unlock()
				return
			}
			if err != nil {
				b.lastError = err.Error()
			}
			b.mu.Unlock()
			select {
			case <-b.done:
				return
			case <-tick.C:
			}
		}
	}()
	return nil
}

func movementCommand(command string) bool {
	switch command {
	case "continue", "next", "step", "stepout", "stepIn", "stepOut":
		return true
	}
	return false
}

// humanIntent is called under b.mu before a human movement can dispatch. A step
// arriving during agent execution requests interruption, then asks the editor to
// retry from the resulting pause rather than stepping an obsolete frame.
func (b *broker) humanIntent(command string) error {
	b.executionIntent++
	b.executionMode = ""
	wasAgent := b.activeTask()
	b.cancelTask("human debugger action")
	moving := b.moving || b.interrupting
	if b.backend != nil {
		moving = moving || truth(b.backend.State()["Running"]) || b.pendingFor(b.backend)
	}
	if wasAgent && movementCommand(command) && moving {
		if err := b.interruptExecution(); err != nil {
			return err
		}
		return fmt.Errorf("agent execution interrupted; retry the human command after the pause settles")
	}
	return nil
}

func (b *broker) setExecutionIntent(command, actor string) {
	b.executionIntent++
	b.executionMode, b.executionActor, b.executionTask = command, actor, ""
	if actor == "agent" && b.s.Task != nil {
		b.executionTask = b.s.Task.ID
	}
}
func (b *broker) mayResumeCapture(intent uint64) bool {
	if b.executionIntent != intent || b.executionMode != "continue" || b.interrupting || b.closing || b.s.Stopped {
		return false
	}
	if human(b.executionActor) {
		return true
	}
	if b.executionActor != "agent" || !b.activeTask() || b.s.Task.ID != b.executionTask || b.s.Binding == nil {
		return false
	}
	return b.taskValid(obj{"task": b.executionTask, "binding": b.s.Binding.ID}) == nil
}
