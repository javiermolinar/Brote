package cli

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type managedClient struct {
	session  session.Descriptor
	consumer session.Consumer
}

func openManaged(s session.Descriptor, id string, r protocol.Recipient) (*managedClient, error) {
	if s.ServiceVersion == 0 {
		return nil, fmt.Errorf("managed events require a shared-service session")
	}
	v, err := api(s, "POST", "/api/action", obj{"action": "consumer-open", "consumer": id, "recipient": r})
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(v["consumer"])
	var c session.Consumer
	if err = json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Instance == "" {
		return nil, fmt.Errorf("broker does not support managed coordination")
	}
	return &managedClient{s, c}, nil
}
func (m *managedClient) request(action string, a obj) (obj, error) {
	if a == nil {
		a = obj{}
	}
	a["action"], a["consumer"], a["instance"] = action, m.consumer.ID, m.consumer.Instance
	return api(m.session, "POST", "/api/action", a)
}
func (m *managedClient) next() (*protocol.DeliveryEnvelope, error) {
	v, err := m.request("consumer-next", nil)
	if err != nil {
		return nil, err
	}
	if v["delivery"] == nil {
		return nil, nil
	}
	data, _ := json.Marshal(v["delivery"])
	var e protocol.DeliveryEnvelope
	if err = json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	if err = e.Check(); err != nil {
		return nil, err
	}
	return &e, nil
}
func (m *managedClient) receipt(e protocol.DeliveryEnvelope, status, detail string) error {
	_, err := m.request("event-status", obj{"kind": e.Kind, "subject": e.Subject, "thread": e.Thread, "attempt": e.Attempt, "status": status, "error": detail})
	return err
}

type managedInput struct {
	Type     string                    `json:"type"`
	Delivery protocol.DeliveryEnvelope `json:"delivery"`
	Status   string                    `json:"status"`
	Error    string                    `json:"error"`
	Fact     protocol.HostFact         `json:"fact"`
}

// managedEvents owns reconnect and receipts. The host supplies only observations
// and external-send outcomes. No adapter keeps delivery state or lease policy.
func managedEvents(s session.Descriptor, id, binding string, in io.Reader, out io.Writer) error {
	if s.Binding == nil || binding != s.Binding.ID {
		return fmt.Errorf("managed events require the current binding")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return managedLoop(ctx, s, id, protocol.Recipient{Kind: "agent", ID: binding, Name: s.Binding.Name, Revision: s.Binding.Revision}, in, out)
}
func managedLoop(ctx context.Context, s session.Descriptor, id string, r protocol.Recipient, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	inputs := make(chan managedInput, 8)
	inputErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var input managedInput
			if err := json.Unmarshal(scanner.Bytes(), &input); err != nil {
				inputErrors <- fmt.Errorf("invalid host frame: %w", err)
				return
			}
			select {
			case inputs <- input:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			inputErrors <- err
		} else {
			inputErrors <- io.EOF
		}
	}()
	encoder := &managedEncoder{ctx: ctx, out: out}
	var m *managedClient
	var inFlight string
	var stopStream context.CancelFunc = func() {}
	defer func() {
		stopStream()
		if m != nil {
			_, _ = m.request("consumer-close", nil)
		}
	}()
	wake := make(chan struct{}, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	type streamFailure struct {
		instance string
		err      error
	}
	streamErrors := make(chan streamFailure, 1)
	connect := func() error {
		stopStream()
		fresh, err := session.Read(s.ID)
		if err == nil {
			s = fresh
		} else if m != nil {
			return err
		}
		if s.Stopped {
			return fmt.Errorf("session ended")
		}
		m, err = openManaged(s, id, r)
		inFlight = ""
		if err != nil {
			return err
		}
		if err = encoder.Encode(obj{"type": "ready", "consumer": m.consumer, "session": s.ID, "run": s.RunID}); err != nil {
			return err
		}
		streamCtx, stop := context.WithCancel(ctx)
		stopStream = stop
		cursor := m.consumer.Cursor
		descriptor := s
		instance := m.consumer.Instance
		go func() {
			err := stream(streamCtx, descriptor, cursor, func() string {
				if r.Kind == "agent" {
					return r.ID
				}
				return ""
			}(), func(session.Event) error { notify(); return nil })
			if streamCtx.Err() == nil {
				select {
				case streamErrors <- streamFailure{instance, err}:
				case <-ctx.Done():
				}
			}
		}()
		if challenge, err := m.request("consumer-challenge", nil); err != nil {
			return err
		} else if err = encoder.Encode(obj{"type": "liveness", "instance": m.consumer.Instance, "challenge": challenge["challenge"]}); err != nil {
			return err
		}
		notify()
		return nil
	}
	reconnect := func() error {
		deadline := time.Now().Add(30 * time.Second)
		for {
			err := connect()
			if err == nil {
				return nil
			}
			var network net.Error
			if !errors.As(err, &network) || time.Now().After(deadline) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	if err := reconnect(); err != nil {
		return err
	}
	// A quiet stream still reconciles durable subjects if their event write failed.
	liveness := time.NewTicker(15 * time.Second)
	defer liveness.Stop()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-inputErrors:
			if err == io.EOF {
				return nil
			}
			return err
		case <-liveness.C:
			challenge, err := m.request("consumer-challenge", nil)
			if err != nil {
				return err
			}
			if err = encoder.Encode(obj{"type": "liveness", "instance": m.consumer.Instance, "challenge": challenge["challenge"]}); err != nil {
				return err
			}
		case input := <-inputs:
			switch input.Type {
			case "liveness":
				challenge, err := m.request("consumer-challenge", nil)
				if err != nil {
					return err
				}
				if err = encoder.Encode(obj{"type": "liveness", "instance": m.consumer.Instance, "challenge": challenge["challenge"]}); err != nil {
					return err
				}
			case "host":
				if input.Fact.Instance != m.consumer.Instance {
					return fmt.Errorf("host instance changed")
				}
				if _, err := m.request("consumer-fact", obj{"fact": input.Fact}); err != nil {
					if err = encoder.Encode(obj{"type": "error", "error": err.Error()}); err != nil {
						return err
					}
				} else if err := encoder.Encode(obj{"type": "host-state", "sequence": input.Fact.Sequence}); err != nil {
					return err
				}

			case "receipt":
				if input.Delivery.Attempt == inFlight {
					inFlight = ""
				}
				if input.Status != "queued" && input.Status != "failed" && input.Status != "unknown" {
					return fmt.Errorf("host must report a send outcome, not an acknowledgement")
				}
				if err := m.receipt(input.Delivery, input.Status, input.Error); err != nil {
					if err = encoder.Encode(obj{"type": "error", "error": err.Error()}); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unsupported host frame %q", input.Type)
			}
			notify()
		case <-tick.C:
			notify()
		case <-wake:
			if inFlight != "" {
				continue
			}
			for {
				e, err := m.next()
				if err != nil {
					var network net.Error
					if !errors.As(err, &network) {
						return err
					}
					if err = reconnect(); err != nil {
						return err
					}
					break
				}
				if e == nil {
					break
				}
				inFlight = e.Attempt
				if err = encoder.Encode(obj{"type": "delivery", "delivery": e}); err != nil {
					return err
				}
				break
			}
		case failure := <-streamErrors:
			if failure.instance != m.consumer.Instance {
				continue
			}
			err := failure.err
			if err != nil && strings.Contains(err.Error(), "binding changed") {
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			if err := reconnect(); err != nil {
				return err
			}
		}
	}
}

// Encode remains cancellable when a host stops consuming stdout. Closing an
// owned pipe on cancellation releases its blocked writer as well.
type managedEncoder struct {
	ctx context.Context
	out io.Writer
}

func (e *managedEncoder) Encode(v any) error {
	done := make(chan error, 1)
	go func() { done <- json.NewEncoder(e.out).Encode(v) }()
	select {
	case err := <-done:
		return err
	case <-e.ctx.Done():
		if c, ok := e.out.(io.Closer); ok {
			_ = c.Close()
		}
		return e.ctx.Err()
	}
}
