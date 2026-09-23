package cli

import (
	"agentdebugger/internal/protocol"
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestRunnerResultAndError(t *testing.T) {
	for _, tc := range []struct {
		result any
		err    error
		code   int
	}{{obj{"ok": true}, nil, 0}, {nil, nil, 0}, {nil, fmt.Errorf("route: %w", &protocol.Error{Code: "stale_revision", Message: "changed"}), 1}} {
		var out, fail bytes.Buffer
		if code := writeResult(tc.result, tc.err, &out, &fail); code != tc.code {
			t.Fatal(code)
		}
		if tc.err != nil {
			var v obj
			if err := json.Unmarshal(fail.Bytes(), &v); err != nil || v["code"] != "stale_revision" || v["version"] != float64(protocol.Version) || out.Len() != 0 {
				t.Fatal(v, err)
			}
		} else if tc.result == nil {
			if out.Len() != 0 || fail.Len() != 0 {
				t.Fatal("nil result emitted output")
			}
		} else {
			var v obj
			if json.Unmarshal(out.Bytes(), &v) != nil || v["ok"] != true || fail.Len() != 0 {
				t.Fatal(out.String(), fail.String())
			}
		}
	}
}
func TestRunnerHelpAndFailure(t *testing.T) {
	var out, fail bytes.Buffer
	if code := Execute([]string{"help", "debug"}, &out, &fail); code != 0 || out.Len() == 0 || fail.Len() != 0 {
		t.Fatal(code, out.String(), fail.String())
	}
	out.Reset()
	if code := Execute([]string{"missing"}, &out, &fail); code != 1 || out.Len() != 0 || !json.Valid(fail.Bytes()) {
		t.Fatal(code, out.String(), fail.String())
	}
}
