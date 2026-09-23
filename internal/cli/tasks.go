package cli

import (
	"agentdebugger/internal/session"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func taskRequest(s session.Descriptor, binding, task, action string, extra obj) (obj, error) {
	state, err := api(s, "GET", "/api/state?brief=1", nil)
	if err != nil {
		return nil, err
	}
	body := obj{"action": action, "actor": "agent", "generation": state["generation"], "binding": binding, "task": task}
	for k, v := range extra {
		body[k] = v
	}
	return api(s, "POST", "/api/action", body)
}

func taskExecute(args []string) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("task-execute requires a session")
	}
	f := flag.NewFlagSet("task-execute", flag.ContinueOnError)
	task := f.String("task", "", "current debugging task ID")
	binding := f.String("binding", "", "expected conversation binding")
	operation := f.String("operation", "", "continue, next, step, stepout or pause")
	wait := f.Duration("wait", 30*time.Second, "maximum execution duration (up to 5m)")
	if err := f.Parse(args[1:]); err != nil {
		return nil, err
	}
	if *task == "" || *binding == "" || f.NArg() != 0 || *wait <= 0 || *wait > 5*time.Minute {
		return nil, fmt.Errorf("task, binding and wait in (0,5m] required")
	}
	switch *operation {
	case "continue", "next", "step", "stepout", "pause":
	default:
		return nil, fmt.Errorf("unsupported execution operation")
	}
	s, err := session.Read(args[0])
	if err != nil {
		return nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return executeTask(ctx, s, *binding, *task, *operation, *wait, 15*time.Second)
}

// A heartbeat belongs to a bounded, live CLI operation, never an idle detached
// listener. Crashed clients therefore cannot keep a grant alive indefinitely.
func executeTask(ctx context.Context, s session.Descriptor, binding, task, operation string, wait, heartbeat time.Duration) (result any, err error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if _, err = taskRequest(s, binding, task, "task-heartbeat", nil); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_, cleanup := taskRequest(s, binding, task, "task-cancel", nil)
			if cleanup != nil {
				err = fmt.Errorf("%w; cancellation: %v", err, cleanup)
			}
		}
	}()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if _, err = taskRequest(s, binding, task, operation, nil); err != nil {
		return nil, err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	lease := time.NewTicker(heartbeat)
	defer lease.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf("execution timed out; task cancelled and pause requested")
		case <-lease.C:
			if _, err = taskRequest(s, binding, task, "task-heartbeat", nil); err != nil {
				return nil, err
			}
		case <-tick.C:
			state, e := api(s, "GET", "/api/state", nil)
			if e != nil {
				return nil, e
			}
			t, _ := state["task"].(map[string]any)
			if str(t["id"]) != task || str(t["status"]) == "cancelled" {
				return nil, fmt.Errorf("execution task changed or cancelled")
			}
			if str(state["status"]) != "running" {
				return summarizeState(state), nil
			}
		}
	}
}
