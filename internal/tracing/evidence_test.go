package tracing

import (
	"testing"
	"time"
)

func TestEvidencePreservesCaptureIdentityAndLegacyRoot(t *testing.T) {
	records := []Record{{Session: "broker:run", Adapter: "brote", Program: "11111111111111111111111111111111", Run: "2222222222222222", Captures: map[string]string{"c": "3333333333333333"}, CaptureDetails: map[string]CaptureDetail{"c": {File: "main.go", Line: 19, Created: time.Now()}}}, {Session: "broker:foreign", Adapter: "native", Program: "11111111111111111111111111111111", Run: "2222222222222222"}}
	targets := evidenceTargets(records, "broker")
	if len(targets) != 2 || targets[0].CaptureID != "" || targets[1].CaptureID != "c" || targets[1].File != "main.go" || targets[1].Line != 19 {
		t.Fatal(targets)
	}
	if len(evidenceTargets(records, "other")) != 0 {
		t.Fatal("cross-owner evidence")
	}
}
