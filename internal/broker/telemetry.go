package broker

import (
	"agentdebugger/internal/session"
	"agentdebugger/internal/telemetry"
	"path/filepath"
	"time"
)

func (b *broker) openTrace(settings *session.LaunchSettings) {
	b.trace = nil
	b.exportError = ""
	if b.s.ServiceVersion == 0 || settings == nil {
		return
	}
	b.exportError = settings.OTLPError
	if settings.OTLP == nil {
		return
	}
	trace, err := telemetry.New(settings.OTLP, b.s.ID, b.s.RunID, filepath.Base(b.s.Binary), b.s.TraceIDs)
	if err != nil {
		b.exportError = err.Error()
		return
	}
	b.trace = trace
	if trace != nil {
		b.s.TraceIDs = trace.IDs
	}
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
