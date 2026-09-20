package dap

import (
	"bufio"
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestDAPFraming(t *testing.T) {
	var buf bytes.Buffer
	want := map[string]any{"type": "event", "event": "stopped", "body": map[string]any{"reason": "断点"}}
	if e := Write(&buf, want); e != nil {
		t.Fatal(e)
	}
	got, e := Read(bufio.NewReader(&buf))
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %v", got)
	}
	for _, frame := range []string{"Content-Length: -1\r\n\r\n", "Content-Length: 999999999\r\n\r\n", "Content-Length: 3\r\n\r\n{}"} {
		if _, e := Read(bufio.NewReader(strings.NewReader(frame))); e == nil {
			t.Fatalf("accepted invalid framing: %q", frame)
		}
	}
}
