package broker

import (
	"agentdebugger/internal/delve"
	"time"
)

// A launch that never finishes editor configuration must not strand a target.
func (b *broker) awaitEditorConfiguration(timeout time.Duration) {
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-b.done:
			return
		case <-timer.C:
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.editorConfigured || b.s.Stopped {
			return
		}
		_ = b.endTargetLocked()
	}()
}

// Caller holds b.mu. Keep response transport alive until the caller replies.
func (b *broker) endTargetLocked() error {
	b.cancelTask("session terminated")
	b.setExecutionIntent("stop", "human")
	_, err := b.rpc("Detach", obj{"Kill": true})
	if _, exited := delve.ExitState(err); err != nil && !exited {
		return err
	}
	b.s.Stopped = true
	b.generation++
	_ = b.persist()
	b.record("terminated", "human", obj{})
	go func() { time.Sleep(150 * time.Millisecond); b.once.Do(func() { close(b.done) }) }()
	return nil
}
