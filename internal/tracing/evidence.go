package tracing

import (
	"agentdebugger/internal/traceinfo"
	"context"
	"sort"
	"strings"
)

type AnnotationEvidence struct {
	traceinfo.Target
	CaptureDetail
}

// EvidenceFor exposes only stored identities belonging to this broker/archive.
// It does not match source locations or infer a span for a legacy pause.
func EvidenceFor(ctx context.Context, id string) ([]AnnotationEvidence, error) {
	records, err := Records(ctx)
	if err != nil {
		return nil, err
	}
	return evidenceTargets(records, id), nil
}
func evidenceTargets(records []Record, id string) []AnnotationEvidence {
	out := []AnnotationEvidence{}
	sort.Slice(records, func(i, j int) bool { return records[i].Started.After(records[j].Started) })
	for _, r := range records {
		if r.Session != id && !(r.Adapter == "brote" && strings.HasPrefix(r.Session, id+":")) {
			continue
		}
		if !traceinfo.HexID(r.Program, 16) {
			continue
		}
		if traceinfo.HexID(r.Run, 8) {
			out = append(out, AnnotationEvidence{Target: traceinfo.Target{Session: r.Session, TraceID: r.Program, SpanID: r.Run}, CaptureDetail: CaptureDetail{Created: r.Started}})
		}
		keys := make([]string, 0, len(r.Captures))
		for k := range r.Captures {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if traceinfo.HexID(r.Captures[k], 8) {
				out = append(out, AnnotationEvidence{Target: traceinfo.Target{Session: r.Session, TraceID: r.Program, SpanID: r.Captures[k], CaptureID: k}, CaptureDetail: r.CaptureDetails[k]})
			}
		}
	}
	return out
}
