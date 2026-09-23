package embeddedtempo

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Running the real process from the test binary also exercises pipe transport and loopback RPC
// startup, dispatch and shutdown under go test -race.
func TestRuntimeProcess(t *testing.T) {
	if dir := os.Getenv("BROTE_RUNTIME_TEST"); dir != "" {
		if err := Run(dir, os.Stdin, os.Stdout); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRuntimeProcess$")
	cmd.Env = append(os.Environ(), "BROTE_RUNTIME_TEST="+t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); _ = cmd.Process.Kill() })
	decoder, encoder := json.NewDecoder(output), json.NewEncoder(input)
	var ready struct {
		Ready bool `json:"ready"`
	}
	if err = decoder.Decode(&ready); err != nil || !ready.Ready {
		t.Fatalf("readiness: %v; %s", err, stderr.String())
	}
	traces := ptrace.NewTraces()
	span := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	id := pcommon.TraceID{1}
	span.SetTraceID(id)
	span.SetSpanID(pcommon.SpanID{1})
	span.SetName("private-process-span")
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now()))
	span.SetEndTimestamp(span.StartTimestamp())
	data, err := (&ptrace.ProtoMarshaler{}).MarshalTraces(traces)
	if err != nil {
		t.Fatal(err)
	}
	if err = encoder.Encode(request{ID: 1, Method: "push", Data: data}); err != nil {
		t.Fatal(err)
	}
	var push response
	if err = decoder.Decode(&push); err != nil || push.ID != 1 || push.Error != "" {
		t.Fatalf("push: %v, %+v", err, push)
	}
	var query response
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if err = encoder.Encode(request{ID: 2, Method: "query", TraceID: hex.EncodeToString(id[:])}); err != nil {
			t.Fatal(err)
		}
		query = response{}
		if err = decoder.Decode(&query); err != nil {
			t.Fatal(err)
		}
		if query.Error == "" {
			break
		}
	}
	if query.ID != 2 || query.Error != "" || !bytes.Contains(query.Result, []byte("private-process-span")) {
		t.Fatalf("query: %+v", query)
	}
	var trace struct {
		Batches []json.RawMessage `json:"batches"`
	}
	if err := json.Unmarshal(query.Result, &trace); err != nil || len(trace.Batches) != 1 {
		t.Fatalf("public trace JSON shape: %v; %s", err, query.Result)
	}
	input.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatalf("runtime exit: %v; %s", err, stderr.String())
	}
}
