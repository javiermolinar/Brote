package cli

import (
	"agentdebugger/internal/tracing"
	"os"
	"path/filepath"
	"testing"
)

func TestAnnotationTargetsRequireExactSavedIdentity(t *testing.T) {
	r := tracing.Record{Session: "broker:run", Program: "11111111111111111111111111111111", Run: "2222222222222222", Captures: map[string]string{"capture": "3333333333333333"}}
	target, err := annotationTarget([]tracing.Record{r}, r.Session, "capture")
	if err != nil || target.SpanID != r.Captures["capture"] || target.CaptureID != "capture" {
		t.Fatal(target, err)
	}
	target, err = annotationTarget([]tracing.Record{r}, r.Session, "")
	if err != nil || target.SpanID != r.Run || target.CaptureID != "" {
		t.Fatal(target, err)
	}
	for _, pair := range [][2]string{{"broker", "capture"}, {r.Session, "missing"}} {
		if _, err := annotationTarget([]tracing.Record{r}, pair[0], pair[1]); err == nil {
			t.Fatal("invented target", pair)
		}
	}
}
func TestAnnotationCLIParsing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "body.txt")
	os.WriteFile(p, []byte("Observed 42"), 0600)
	in, capture, err := parseAnnotation("run", []string{"--id", "note", "--body-file", p, "--capture", "c", "--comparison-key", "total"})
	if err != nil || in.Body != "Observed 42" || in.ID != "note" || in.Revision != 1 || capture != "c" {
		t.Fatal(in, err)
	}
	for _, args := range [][]string{{"--body", "text"}, {"--id", "n", "--body", " "}, {"--id", "n", "--body", "text", "--body-file", p}, {"--id", "n", "--body", "text", "--capture", "c", "--targets-file", p}, {"--id", "n", "--body", "text", "extra"}} {
		if _, _, err := parseAnnotation("run", args); err == nil {
			t.Fatal(args)
		}
	}
}
