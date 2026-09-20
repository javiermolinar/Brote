package broker

import "testing"

func TestEditorHandlesRejectPreviousPause(t *testing.T) {
	p := &dapPeer{}
	body := obj{"stackFrames": []any{obj{"id": 1000}}}
	p.exportHandles(body, "stackTrace", 10)
	exported := asObj(asList(body["stackFrames"])[0])["id"]
	args := obj{"frameId": exported}
	if e := p.translateArguments(args, 10); e != nil || num(args["frameId"]) != 1000 {
		t.Fatal(args, e)
	}
	if e := p.translateArguments(obj{"frameId": exported}, 11); e == nil {
		t.Fatal("old frame accepted")
	}
	current := obj{"stackFrames": []any{obj{"id": 1000}}}
	p.exportHandles(current, "stackTrace", 11)
	if asObj(asList(current["stackFrames"])[0])["id"] == exported {
		t.Fatal("backend handle reuse leaked to editor")
	}
}
