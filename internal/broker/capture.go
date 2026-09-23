package broker

import (
	"agentdebugger/internal/protocol"
	"agentdebugger/internal/session"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type captureRecord struct {
	protocol.CaptureOutcome
	Name     string `json:"name"`
	Created  string `json:"created"`
	Snapshot obj    `json:"snapshot,omitempty"`
	Values   obj    `json:"values,omitempty"`
}

// captureStop runs under the coordinator lock. Each hit is reserved before I/O;
// recovery never silently resets a definition's per-run capture limit.
func (b *broker) captureStop() {
	attribution := b.currentStop
	if !attribution.Eligible || b.s.ServiceVersion == 0 || b.closing || b.s.Stopped {
		return
	}
	if b.capturePending >= 8 {
		b.lastError = "capture capacity exhausted; target remains paused"
		return
	}
	source, epoch, backendEpoch, run, revision := b.backend, b.handleEpoch, b.backend.Epoch(), b.s.RunID, b.s.Definitions.Revision
	identity := b.identity()
	intent := b.executionIntent
	descriptorData, err := json.Marshal(b.s)
	if err != nil {
		b.lastError = err.Error()
		return
	}
	var descriptor session.Descriptor
	if err = json.Unmarshal(descriptorData, &descriptor); err != nil {
		b.lastError = err.Error()
		return
	}
	records := []captureRecord{}
	if b.s.CaptureCounts == nil {
		b.s.CaptureCounts = map[string]int{}
	}
	for _, definition := range attribution.Definitions {
		b.s.CaptureSequence++
		record := captureRecord{CaptureOutcome: protocol.CaptureOutcome{Identity: identity, ID: session.NewID(16), DefinitionID: definition.ID, Sequence: b.s.CaptureSequence, Goroutine: attribution.Goroutine, ProgramTraceID: b.s.TraceIDs.Program, DebuggerTraceID: b.s.TraceIDs.Debugger, Status: "captured", ExportStatus: "disabled"}, Name: definition.Name, Created: time.Now().UTC().Format(time.RFC3339Nano)}
		if b.s.CaptureCounts[definition.ID] >= definition.CaptureLimit {
			record.Status = "skipped"
			record.Error = &protocol.Error{Code: "capture_limit", Message: "per-run capture limit exhausted"}
		} else {
			b.s.CaptureCounts[definition.ID]++
		}
		records = append(records, record)
	}
	if err := b.persist(); err != nil {
		b.lastError = err.Error()
		return
	}
	b.capturePending++
	b.captureWorkers.Add(1)
	go func() {
		defer b.captureWorkers.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		reader := &broker{s: descriptor, backend: source, generation: identity.Generation, inspectionContext: ctx}
		state := source.State()
		for i := range records {
			record := &records[i]
			if record.Status == "skipped" {
				continue
			}
			view := obj{"id": identity.Session, "run": run, "generation": identity.Generation, "pauseEpoch": epoch, "status": "paused", "state": pick(state, "stopReason", "stopDescription", "stopText")}
			snapshot, err := reader.enrichSnapshot(view, state, attribution.Goroutine, 0)
			record.Snapshot = snapshot
			if err == nil && len(asList(snapshot["frames"])) == 0 {
				err = fmt.Errorf("hit goroutine has no stack")
			}
			record.Values = obj{}
			definition := attribution.Definitions[i]
			names := []string{}
			for name := range definition.Values {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				value, e := reader.evaluate(definition.Values[name], attribution.Goroutine, 0, 2, 32, state)
				if e != nil {
					record.Values[name] = obj{"error": e.Error()}
					err = e
				} else {
					record.Values[name] = value["value"]
				}
			}
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			if err != nil {
				record.Status = "failed"
				record.Error = &protocol.Error{Code: "capture_failed", Message: err.Error()}
			} else if qualityProblem(record.Snapshot) || qualityProblem(record.Values) {
				record.Status = "truncated"
			}
			payload, _ := json.Marshal(record)
			if len(payload) > 128<<10 {
				record.Snapshot = nil
				record.Values = nil
				record.Status = "truncated"
				record.Error = &protocol.Error{Code: "capture_size", Message: "capture exceeded 128 KiB payload limit"}
			}
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.capturePending--
		current := b.backend == source && b.handleEpoch == epoch && source.Epoch() == backendEpoch && b.s.RunID == run && b.s.Definitions.Revision == revision && b.generation == identity.Generation && !b.moving && !b.closing
		success := current
		for i := range records {
			if !current {
				records[i].Status = "failed"
				records[i].Snapshot = nil
				records[i].Values = nil
				records[i].Error = &protocol.Error{Code: "stale_capture", Message: "target or definitions changed during capture"}
			}
			b.appendCapture(records[i])
			if records[i].Status != "captured" {
				success = false
			}
		}
		if success && b.mayResumeCapture(intent) {
			if err := b.dispatchDAP("continue", obj{"threadId": attribution.Goroutine}, nil); err != nil {
				b.lastError = err.Error()
				b.executionMode = ""
			}
		} else if b.executionIntent == intent {
			b.executionMode = ""
		}
	}()
}
func qualityProblem(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if (key == "truncated" || key == "localsTruncated") && truth(item) {
				return true
			}
			if (key == "error" || key == "inspectionError" || key == "localsError" || key == "exceptionError" || key == "unreadable") && str(item) != "" {
				return true
			}
			if qualityProblem(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if qualityProblem(item) {
				return true
			}
		}
	}
	return false
}
func (b *broker) appendCapture(record captureRecord) {
	b.exportCapture(&record)
	b.captures = append(b.captures, record)
	if len(b.captures) > 64 {
		b.captures = b.captures[len(b.captures)-64:]
	}
	b.record("tracepoint.capture", "debugger", record)
	_ = b.emit("capture_"+record.Status, record.ID)
}
