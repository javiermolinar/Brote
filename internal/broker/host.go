package broker

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"encoding/json"
	"fmt"
	"time"
)

const hostProofTTL = 30 * time.Second
const hostChallengeTTL = 20 * time.Second

func (b *broker) hostAction(c session.Consumer, a obj) (obj, error) {
	now := time.Now()
	switch str(a["action"]) {
	case "consumer-challenge":
		old := c
		c.Challenge = session.NewID(16)
		c.ChallengeExpires = now.Add(hostChallengeTTL).UTC().Format(time.RFC3339Nano)
		b.s.Consumers[c.ID] = c
		if err := b.persist(); err != nil {
			b.s.Consumers[c.ID] = old
			return nil, err
		}
		return obj{"instance": c.Instance, "challenge": c.Challenge}, nil
	case "consumer-close":
		if b.activeTask() && b.s.Task.HostConsumer == c.ID && b.s.Task.HostInstance == c.Instance {
			b.cancelTask("host closed")
			if err := b.interruptExecution(); err != nil {
				return nil, err
			}
		}
		subjects, err := b.deliverySubjects()
		if err != nil {
			return nil, err
		}
		for _, subject := range subjects {
			if subject.status == "sending" && subject.attempt != nil && subject.attempt.Consumer == c.ID && subject.attempt.Instance == c.Instance {
				if err := subject.save("unknown", subject.attempt, "host closed during send; inspect conversation before retrying"); err != nil {
					return nil, err
				}
			}
		}
		delete(b.s.Consumers, c.ID)
		if err := b.persist(); err != nil {
			b.s.Consumers[c.ID] = c
			return nil, err
		}
		return obj{"status": "closed"}, nil
	case "consumer-fact":
		var fact protocol.HostFact
		data, _ := json.Marshal(a["fact"])
		if err := json.Unmarshal(data, &fact); err != nil {
			return nil, err
		}
		if err := fact.Check(); err != nil {
			return nil, err
		}
		if fact.Instance != c.Instance || (c.Host != nil && fact.Sequence <= c.Host.Sequence) {
			return nil, fmt.Errorf("obsolete host fact")
		}
		// Fence expired scope before accepting proof that could make it appear
		// live again. The maintenance timer is not an authority boundary.
		if t := b.s.Task; b.activeTask() && t.HostConsumer == c.ID && t.HostInstance == c.Instance && !b.hostTaskValid(t, now) {
			b.cancelTask("host proof expired")
			if err := b.interruptExecution(); err != nil {
				return nil, err
			}
		}
		old := c
		host := &session.HostState{HostFact: fact}
		if c.Host != nil && c.Host.Turn == fact.Turn && c.Host.State == "active" && fact.State == "active" {
			host.Expires = c.Host.Expires
		}
		if fact.Challenge != "" {
			deadline, err := time.Parse(time.RFC3339Nano, c.ChallengeExpires)
			if err != nil || fact.Challenge != c.Challenge || !now.Before(deadline) {
				return nil, fmt.Errorf("host challenge expired or changed")
			}
			if fact.State == "active" {
				host.Expires = now.Add(hostProofTTL).UTC().Format(time.RFC3339Nano)
			}
			c.Challenge = ""
			c.ChallengeExpires = ""
		}
		c.Host = host
		b.s.Consumers[c.ID] = c
		if err := b.persist(); err != nil {
			b.s.Consumers[c.ID] = old
			return nil, err
		}
		if t := b.s.Task; b.activeTask() && t.HostConsumer == c.ID && t.HostInstance == c.Instance {
			if fact.State != "active" || fact.Turn != t.HostTurn {
				// Settled hosts complete only when the same scoped turn is paused.
				if fact.State == "idle" && fact.Turn == t.HostTurn {
					state, err := b.state()
					if err == nil && stateStatus(state, b.moving) == "paused" && !truth(state["NextInProgress"]) && !b.interrupting {
						previous := *t
						copy := *t
						copy.Status = "completed"
						b.s.Task = &copy
						if err := b.emit("task.completed", t.ID); err != nil {
							b.s.Task = &previous
							return nil, err
						}
						b.record("task.completed", "host", b.s.Task)
						return obj{"task": b.taskView()}, nil
					}
				}
				b.cancelTask("host turn ended or changed")
				if err := b.interruptExecution(); err != nil {
					return nil, err
				}
			}
		}
		return obj{"host": host}, nil
	}
	return nil, fmt.Errorf("unknown host action")
}
func (b *broker) hostTaskValid(t *session.ExecutionTask, now time.Time) bool {
	if t.HostConsumer == "" {
		return true
	}
	c, ok := b.s.Consumers[t.HostConsumer]
	if !ok || c.Instance != t.HostInstance || c.Host == nil || c.Host.State != "active" || c.Host.Turn != t.HostTurn || !sameRecipient(c.Recipient, agentRecipient(t.Binding)) {
		return false
	}
	deadline, err := time.Parse(time.RFC3339Nano, c.Host.Expires)
	return err == nil && now.Before(deadline)
}
func (b *broker) associateHost(a obj) error {
	if str(a["consumer"]) == "" {
		return nil
	}
	c, ok := b.s.Consumers[str(a["consumer"])]
	t := b.s.Task
	if !ok || c.Instance != str(a["instance"]) || c.Host == nil || c.Host.Turn != str(a["turn"]) || c.Host.State != "active" || !sameRecipient(c.Recipient, agentRecipient(t.Binding)) {
		return fmt.Errorf("current active host turn required")
	}
	candidate := *t
	candidate.HostConsumer = c.ID
	candidate.HostInstance = c.Instance
	candidate.HostTurn = c.Host.Turn
	if !b.hostTaskValid(&candidate, time.Now()) {
		return fmt.Errorf("fresh host liveness proof required")
	}
	if t.HostConsumer != "" && (t.HostConsumer != candidate.HostConsumer || t.HostInstance != candidate.HostInstance || t.HostTurn != candidate.HostTurn) {
		return fmt.Errorf("task belongs to another host turn")
	}
	b.s.Task = &candidate
	return nil
}
