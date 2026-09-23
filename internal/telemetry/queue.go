package telemetry

import (
	"context"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	"sync"
	"time"
)

// A bounded processor makes queue exhaustion and transport failure observable;
// the SDK batch processor otherwise drops overflowing spans without capture IDs.
type queue struct {
	mu          sync.Mutex
	exporter    sdk.SpanExporter
	items       chan sdk.ReadOnlySpan
	done        chan struct{}
	cancel      context.CancelFunc
	closed      bool
	active      bool
	states      map[string]string
	order       []string
	failures    int
	outstanding int
}

func newQueue(exporter sdk.SpanExporter) *queue {
	ctx, cancel := context.WithCancel(context.Background())
	q := &queue{exporter: exporter, items: make(chan sdk.ReadOnlySpan, 256), done: make(chan struct{}), cancel: cancel, states: map[string]string{}}
	go q.run(ctx)
	return q
}
func captureID(s sdk.ReadOnlySpan) string {
	for _, a := range s.Attributes() {
		if string(a.Key) == "program.capture.id" {
			return a.Value.AsString()
		}
	}
	return ""
}
func (q *queue) state(id, status string) {
	if id == "" {
		return
	}
	if _, ok := q.states[id]; !ok {
		q.order = append(q.order, id)
	}
	q.states[id] = status
	if len(q.order) > 1024 {
		delete(q.states, q.order[0])
		q.order = q.order[1:]
	}
}
func (q *queue) OnStart(context.Context, sdk.ReadWriteSpan) {}
func (q *queue) OnEnd(span sdk.ReadOnlySpan) {
	q.mu.Lock()
	defer q.mu.Unlock()
	id := captureID(span)
	if q.closed {
		q.state(id, "failed")
		q.failures++
		return
	}
	select {
	case q.items <- span:
		q.outstanding++
		q.state(id, "queued")
	default:
		q.state(id, "failed")
		q.failures++
	}
}
func (q *queue) run(ctx context.Context) {
	defer close(q.done)
	for first := range q.items {
		batch := []sdk.ReadOnlySpan{first}
	drain:
		for len(batch) < 64 {
			select {
			case span, ok := <-q.items:
				if !ok {
					break drain
				}
				batch = append(batch, span)
			default:
				break drain
			}
		}
		q.mu.Lock()
		q.active = true
		q.mu.Unlock()
		deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := q.exporter.ExportSpans(deadline, batch)
		cancel()
		q.mu.Lock()
		q.active = false
		q.outstanding -= len(batch)
		status := "sent"
		if err != nil {
			status = "failed"
			q.failures += len(batch)
		}
		for _, span := range batch {
			q.state(captureID(span), status)
		}
		q.mu.Unlock()
	}
}
func (q *queue) Shutdown(ctx context.Context) error {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.items)
	}
	q.mu.Unlock()
	select {
	case <-q.done:
		q.cancel()
		return q.exporter.Shutdown(ctx)
	case <-ctx.Done():
		q.cancel()
		return ctx.Err()
	}
}
func (q *queue) ForceFlush(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		q.mu.Lock()
		idle := q.outstanding == 0
		q.mu.Unlock()
		if idle {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (q *queue) Status(id string) string { q.mu.Lock(); defer q.mu.Unlock(); return q.states[id] }
func (q *queue) Failures() int           { q.mu.Lock(); defer q.mu.Unlock(); return q.failures }
