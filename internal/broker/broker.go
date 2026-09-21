package broker

import (
	"sync"
	"time"

	"agentdebugger/internal/backend"
	"agentdebugger/internal/delve"
	"agentdebugger/internal/session"
)

type broker struct {
	interrupting      bool
	agentStreams      int
	agentDisconnected time.Time
	history           *session.History
	historyError      string
	stopID            string
	captured          map[string]bool
	backend           *backend.Delve
	changed           chan struct{}
	mu                sync.Mutex
	s                 session.Descriptor
	rpcAddr           string
	owner             string
	generation        int
	handleEpoch       int
	moving            bool
	lastError         string
	peer              *dapPeer
	done              chan struct{}
	once              sync.Once
}

func (b *broker) rpc(method string, arg any) (obj, error) {
	if b.backend != nil {
		return b.backend.Call(method, asObj(arg))
	}
	return delve.Call(b.rpcAddr, method, arg, 5*time.Second)
}

func (b *broker) state() (obj, error) {
	v, e := b.rpc("State", obj{"NonBlocking": true})
	if s, ok := delve.ExitState(e); ok {
		return s, nil
	}
	s := asObj(v["State"])
	if e == nil && truth(s["Running"]) && num(s["Pid"]) == 0 {
		s["Pid"] = b.s.TargetPID
	}

	return s, e
}

func stateStatus(s obj, moving bool) string {
	if truth(s["exited"]) {
		return "exited"
	}
	if truth(s["Running"]) || moving {
		return "running"
	}
	return "paused"
}
