package embeddedtempo

import (
	"testing"
	"time"
)

func TestShutdownDrainsDirectConsumers(t *testing.T) {
	s := &loopbackServer{}
	if !s.beginServing() {
		t.Fatal("consumer rejected before shutdown")
	}
	stopped := make(chan struct{})
	go func() { s.SetKeepAlivesEnabled(false); close(stopped) }()
	deadline := time.Now().Add(time.Second)
	for {
		s.admission.Lock()
		stopping := s.stopping
		s.admission.Unlock()
		if stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if s.beginServing() {
		t.Fatal("consumer admitted during shutdown")
	}
	select {
	case <-stopped:
		t.Fatal("Tempo shutdown overtook the active consumer")
	default:
	}
	s.consumers.Done()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after drain")
	}
}
