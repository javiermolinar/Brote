package cli

import (
	"agentdebugger/internal/traceinfo"
	"agentdebugger/internal/tracing"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Annotation operations only contact the trace service, including for saved runs.
func annotationCommand(verb string, args []string) (any, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, fmt.Errorf("exact trace service SESSION required (see query traces)")
	}
	owner := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if verb == "list" {
		if len(args) != 1 {
			return nil, fmt.Errorf("unexpected arguments")
		}
		return tracing.Annotations(ctx, owner)
	}
	in, capture, err := parseAnnotation(owner, args[1:])
	if err != nil {
		return nil, err
	}
	if len(in.Targets) == 0 {
		records, err := tracing.Records(ctx)
		if err != nil {
			return nil, err
		}
		target, err := annotationTarget(records, owner, capture)
		if err != nil {
			return nil, err
		}
		in.Targets = []traceinfo.Target{target}
	}
	return tracing.Annotate(ctx, in)
}
func parseAnnotation(owner string, args []string) (tracing.AnnotationRequest, string, error) {
	in := tracing.AnnotationRequest{Session: owner}
	f := newFlagSet("annotation create")
	f.StringVar(&in.ID, "id", "", "Stable annotation ID; reuse on retry")
	f.Uint64Var(&in.Revision, "revision", 1, "Append-only revision")
	f.StringVar(&in.Author, "author", "agent", "Author display name")
	f.StringVar(&in.Body, "body", "", "Annotation text")
	bodyFile := f.String("body-file", "", "UTF-8 body file")
	capture := f.String("capture", "", "Persisted capture ID; omit to target program root")
	targets := f.String("targets-file", "", "JSON evidence target array, including owner, for comparisons")
	conversation := f.String("conversation-file", "", "JSON conversation target")
	f.StringVar(&in.Label, "label", "", "Optional label")
	f.StringVar(&in.ComparisonKey, "comparison-key", "", "Optional comparison key")
	if err := f.Parse(args); err != nil {
		return in, "", err
	}
	if f.NArg() != 0 || in.ID == "" || in.Revision == 0 {
		return in, "", fmt.Errorf("--id and a positive revision are required; no positional arguments")
	}
	if *bodyFile != "" {
		if in.Body != "" {
			return in, "", fmt.Errorf("choose --body or --body-file")
		}
		data, err := os.ReadFile(*bodyFile)
		if err != nil {
			return in, "", err
		}
		in.Body = string(data)
	}
	if strings.TrimSpace(in.Body) == "" {
		return in, "", fmt.Errorf("non-empty --body or --body-file required")
	}
	if *targets != "" {
		if *capture != "" {
			return in, "", fmt.Errorf("choose --capture or --targets-file")
		}
		data, err := os.ReadFile(*targets)
		if err != nil {
			return in, "", err
		}
		if err = json.Unmarshal(data, &in.Targets); err != nil {
			return in, "", err
		}
		if len(in.Targets) == 0 {
			return in, "", fmt.Errorf("targets file must contain evidence")
		}
	}
	if *conversation != "" {
		data, err := os.ReadFile(*conversation)
		if err != nil {
			return in, "", err
		}
		if err = json.Unmarshal(data, &in.Conversation); err != nil {
			return in, "", err
		}
		if in.Conversation == nil {
			return in, "", fmt.Errorf("conversation target required")
		}
	}
	return in, *capture, nil
}
func annotationTarget(records []tracing.Record, owner, capture string) (traceinfo.Target, error) {
	for _, r := range records {
		if r.Session != owner {
			continue
		}
		span := r.Run
		if capture != "" {
			span = r.Captures[capture]
			if span == "" {
				return traceinfo.Target{}, fmt.Errorf("capture has no persisted span identity")
			}
		}
		if !traceinfo.HexID(r.Program, 16) || !traceinfo.HexID(span, 8) {
			return traceinfo.Target{}, fmt.Errorf("saved evidence has no verified trace identity")
		}
		return traceinfo.Target{Session: owner, TraceID: r.Program, SpanID: span, CaptureID: capture}, nil
	}
	return traceinfo.Target{}, fmt.Errorf("exact trace service session unavailable; use query traces")
}
