package broker

import (
	"sync"
	"time"

	"debug-handover/internal/delve"
	"debug-handover/internal/session"
)

type broker struct {
	mu         sync.Mutex
	s          session.Descriptor
	rpcAddr    string
	owner      string
	generation int
	moving     bool
	lastError  string
	peer       *dapPeer
	done       chan struct{}
	once       sync.Once
}

func (b *broker) rpc(method string, arg any) (obj, error) {
	return delve.Call(b.rpcAddr, method, arg, 5*time.Second)
}

func (b *broker) state() (obj, error) {
	v, e := b.rpc("State", obj{"NonBlocking": true})
	if s, ok := delve.ExitState(e); ok {
		return s, nil
	}
	return asObj(v["State"]), e
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
