package backend

import (
	"agentdebugger/internal/dap"
	"testing"
)

func TestReviewExitCodeSurvivesTermination(t *testing.T) {
	events := make(chan map[string]any, 2)
	d := &Delve{client: &dap.Client{Events: events}, state: obj{}, stopped: make(chan struct{}), done: make(chan struct{}), Events: make(chan obj, 2)}
	events <- obj{"event": "exited", "body": obj{"exitCode": 23}}
	events <- obj{"event": "terminated", "body": obj{}}
	close(events)
	d.events()
	if d.State()["exitStatus"] != 23 {
		t.Fatalf("exit status lost after terminated: %#v", d.State())
	}
}
