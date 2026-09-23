package broker

import (
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Called with b.mu held; releases it while collecting evidence and reacquires it
// before returning. Only an immutable descriptor copy is used outside the lock.
func (b *broker) inspectSnapshot(v, s obj, gid, frame int) (obj, error) {
	source, epoch, backendEpoch, run := b.backend, b.handleEpoch, b.backend.Epoch(), b.s.RunID
	data, err := json.Marshal(b.s)
	if err != nil {
		return nil, err
	}
	var descriptor session.Descriptor
	if err = json.Unmarshal(data, &descriptor); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reader := &broker{s: descriptor, backend: source, generation: b.generation, inspectionContext: ctx}
	b.mu.Unlock()
	result, err := reader.enrichSnapshot(v, s, gid, frame)
	b.mu.Lock()
	if b.backend != source || b.handleEpoch != epoch || source.Epoch() != backendEpoch || b.s.RunID != run || b.moving || b.closing {
		err = fmt.Errorf("target changed during inspection; refresh the paused state")
		result = nil
	} else if err == nil {
		if ctx.Err() != nil {
			result["inspectionError"] = ctx.Err().Error()
			result["truncated"] = true
		}
		b.historyInspection(result)
	}
	return result, err
}

// Inspection replies must not block the frontend from reading a later Pause.
// Limits bound work per request, and each connection admits at most eight reads.
func (b *broker) inspectEditor(p *dapPeer, request obj, command string, args obj) error {
	if b.moving || b.interrupting {
		return fmt.Errorf("inspection requires a settled pause")
	}
	if p.inspections >= 8 {
		return fmt.Errorf("too many pending inspection requests")
	}
	if err := p.translateArguments(args, b.handleEpoch); err != nil {
		return err
	}
	if command == "evaluate" {
		if err := validateExpression(str(args["expression"])); err != nil {
			return err
		}
	}
	switch command {
	case "stackTrace":
		args["levels"] = min(128, max(1, num(args["levels"])))
		if num(args["startFrame"]) < 0 {
			return fmt.Errorf("invalid stack offset")
		}
	case "variables":
		args["count"] = min(128, max(1, num(args["count"])))
		if num(args["start"]) < 0 {
			return fmt.Errorf("invalid variable offset")
		}
	}
	source, epoch, run := b.backend, b.handleEpoch, b.s.RunID
	p.inspections++
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		body, err := source.Inspect(ctx, command, args)
		b.mu.Lock()
		p.inspections--
		if b.peer != p || b.backend != source || b.handleEpoch != epoch || b.s.RunID != run || b.moving || b.closing {
			body = nil
			err = fmt.Errorf("target changed during inspection; refresh the paused state")
		}
		if err == nil {
			// Some adapters ignore requested paging. Never export unbounded results.
			for _, key := range []string{"stackFrames", "scopes", "variables", "threads"} {
				items := asList(body[key])
				limit := 128
				if key == "threads" {
					limit = 256
				}
				if len(items) > limit {
					body[key] = items[:limit]
					body["broteTruncated"] = true
				}
			}
			raw, _ := json.Marshal(body)
			if len(raw) > 256<<10 {
				body = nil
				err = fmt.Errorf("inspection exceeds 256 KiB response limit; request fewer values")
			} else {
				p.exportHandles(body, command, epoch)
			}
		}
		b.mu.Unlock()
		response := obj{"type": "response", "request_seq": request["seq"], "command": command, "success": err == nil, "body": body}
		if err != nil {
			response["message"] = err.Error()
		}
		_ = p.send(response)
	}()
	return nil
}

// evaluateService follows the same epoch fence as snapshots without holding the
// coordinator mutex while Delve expands a value.
func (b *broker) evaluateService(a, s obj) (obj, error) {
	source, epoch, backendEpoch, run := b.backend, b.handleEpoch, b.backend.Epoch(), b.s.RunID
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reader := &broker{backend: source, generation: b.generation, inspectionContext: ctx}
	b.mu.Unlock()
	value, err := reader.evaluate(str(a["expression"]), num(a["goroutine"]), num(a["frame"]), num(a["depth"]), num(a["count"]), s)
	b.mu.Lock()
	if b.backend != source || b.handleEpoch != epoch || source.Epoch() != backendEpoch || b.s.RunID != run || b.moving || b.closing {
		return nil, fmt.Errorf("target changed during inspection; refresh the paused state")
	}
	if err == nil {
		value["run"], value["pauseEpoch"] = run, epoch
	}
	return value, err
}

func (b *broker) serviceInspect(r serviceRequest) (obj, error) {
	if b.backend == nil || b.moving || b.interrupting || truth(b.backend.State()["Running"]) {
		return nil, fmt.Errorf("inspection requires a settled shared-service pause")
	}
	if r.Start < 0 || r.Count < 1 || r.Count > 128 || r.Goroutine < 0 {
		return nil, fmt.Errorf("start/goroutine must be nonnegative and count must be 1–128")
	}
	source, epoch, backendEpoch, identity := b.backend, b.handleEpoch, b.backend.Epoch(), b.identity()
	method, args := "ListGoroutines", obj{"Start": r.Start, "Count": r.Count}
	if r.Operation == "inspection.stack" {
		method = "Stacktrace"
		args = obj{"Id": r.Goroutine, "Start": r.Start, "Depth": r.Count}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.mu.Unlock()
	body, err := source.CallContext(ctx, method, args)
	b.mu.Lock()
	if b.backend != source || b.handleEpoch != epoch || source.Epoch() != backendEpoch || b.moving || b.closing {
		return nil, fmt.Errorf("target changed during inspection")
	}
	return obj{"version": 1, "identity": identity, "result": body}, err
}
