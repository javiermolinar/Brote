package broker

import (
	"agentdebugger/internal/session"
	"agentdebugger/internal/telemetry"
	"agentdebugger/internal/tracing"
	"context"
	"path/filepath"
	"time"
)

func (b *broker) openTrace(settings *session.LaunchSettings) {
	b.trace = nil
	b.exportError = ""
	if b.s.ServiceVersion == 0 || settings == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	endpoint, err := tracing.EnsureSpanService(ctx)
	cancel()
	if err != nil {
		b.exportError = err.Error()
		return
	}
	exporter := tracing.NewSpanExporter(endpoint)
	trace := telemetry.NewWithExporter(exporter, b.s.ID, b.s.RunID, filepath.Base(b.s.Binary), b.s.TraceIDs)
	exporter.Record = tracing.Record{Session: b.s.ID + ":" + b.s.RunID, Name: filepath.Base(b.s.Binary), Adapter: "brote", Program: trace.IDs.Program, Debugger: trace.IDs.Debugger, Run: trace.IDs.ProgramRoot, Root: trace.IDs.DebuggerRoot, Started: time.Now()}
	exporter.Start()
	b.trace = trace
	b.s.TraceIDs = trace.IDs

}
func (b *broker) exportCapture(record *captureRecord) {
	if record.Run != b.s.RunID {
		record.ExportStatus = "skipped"
		return
	}
	if b.trace == nil {
		record.ExportStatus = "disabled"
		if b.exportError != "" {
			record.ExportStatus = "failed"
		}
		return
	}
	record.ProgramTraceID, record.DebuggerTraceID = b.trace.IDs.Program, b.trace.IDs.Debugger
	if record.Status != "captured" {
		record.ExportStatus = "skipped"
		return
	}
	created, err := time.Parse(time.RFC3339Nano, record.Created)
	if err != nil {
		record.ExportStatus = "failed"
		return
	}
	record.ExportStatus = b.trace.Capture(record.ID, record.Name, record.Sequence, record.Goroutine, record.Snapshot, record.Values, created)
}
func (b *broker) captureView() []captureRecord {
	result := append([]captureRecord{}, b.captures...)
	for i := range result {
		if result[i].ExportStatus == "queued" && b.trace != nil {
			if status := b.trace.Status(result[i].ID); status != "" {
				result[i].ExportStatus = status
			}
		}
	}
	return result
}
