package session

import (
	"encoding/json"
	"testing"
)

func TestDecodeDiscussionRequest(t *testing.T) {
	const revision = uint64(1<<63 + 1)
	r, err := DecodeDiscussionRequest(map[string]any{"action": "reply", "revision": revision, "recipient": map[string]any{"kind": "agent", "id": "a", "revision": revision}})
	if err != nil || r.Revision != revision || r.Recipient == nil || r.Recipient.Revision != revision {
		t.Fatalf("lost exact revision or recipient: %+v %v", r, err)
	}
	for _, value := range []any{-1, 1.5, "1", json.Number("18446744073709551616")} {
		if _, err := DecodeDiscussionRequest(map[string]any{"revision": value}); err == nil {
			t.Fatalf("accepted revision %v", value)
		}
	}
	for _, fields := range []map[string]any{{"recipient": 3}, {"recipient": map[string]any{"revision": -1}}, {"body": make(chan int)}} {
		if _, err := DecodeDiscussionRequest(fields); err == nil {
			t.Fatalf("accepted malformed request %v", fields)
		}
	}
}
