package traceinfo

import (
	"strings"
	"testing"
	"time"
)

func TestMetadataIdentityAndBounds(t *testing.T) {
	r := Record{ID: "note", Revision: 1, Kind: "annotation", Session: "session:run", TraceID: strings.Repeat("a", 32), ParentID: strings.Repeat("b", 16), Created: time.Now(), Author: "human", Body: "evidence"}
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
	id := r.SpanID()
	if r.SpanID() != id {
		t.Fatal("unstable retry identity")
	}
	r.Revision++
	if r.SpanID() == id {
		t.Fatal("revision must create another span")
	}
	r.Body = strings.Repeat("界", 6000)
	if r.Check() == nil {
		t.Fatal("accepted oversized annotation")
	}
	r.Body, r.Truncated = BoundBody(r.Body)
	if !r.Truncated || r.Check() != nil {
		t.Fatal("invalid truncated UTF-8")
	}
	r.Targets = []Target{{Session: "other", TraceID: strings.Repeat("0", 32)}}
	if r.Check() == nil {
		t.Fatal("accepted zero trace identity")
	}
}
