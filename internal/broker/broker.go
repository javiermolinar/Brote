package broker

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"agentdebugger/internal/backend"
	"agentdebugger/internal/delve"
	"agentdebugger/internal/session"
	"agentdebugger/internal/telemetry"
	"agentdebugger/internal/tracing"
)

type broker struct {
	traces            *tracing.Recorder
	traceSequence     int
	editorConfigured  bool
	trace             *telemetry.Session
	exportError       string
	captureWorkers    sync.WaitGroup
	executionIntent   uint64
	executionMode     string
	executionActor    string
	executionTask     string
	captures          []captureRecord
	capturePending    int
	currentStop       stopAttribution
	resolutions       []definitionResolution
	inspectionContext context.Context
	closing           bool
	seenCommands      map[string]bool
	dispatchSequence  uint64
	pendingExecution  map[uint64]*backend.Delve

	process           *exec.Cmd
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
		if b.inspectionContext != nil {
			return b.backend.CallContext(b.inspectionContext, method, asObj(arg))
		}
		return b.backend.Call(method, asObj(arg))
	}
	return delve.Call(b.rpcAddr, method, arg, 5*time.Second)
}

func (b *broker) state() (obj, error) {
	if b.s.ServiceVersion > 0 && b.backend != nil {
		return b.backend.State(), nil
	}
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
