package tracing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentdebugger/internal/embeddedtempo"
	"agentdebugger/internal/session"
	"github.com/grafana/tempo/v3/pkg/tempopb"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var headerPattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9a-zA-Z-]+$")
var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

const accepted = "Export accepted; query to verify"
const failed = "Export failed or incomplete"

type batch struct {
	session string
	traces  ptrace.Traces
	barrier chan struct{}
}
type engine struct {
	mu            sync.Mutex
	dir           string
	captures      map[string]*capture
	local, remote chan batch
	workers       sync.WaitGroup
	queries       http.Handler
	remoteError   bool
	last          time.Time
}

func recordPath(dir, id string) string {
	h := sha256.Sum256([]byte(id))
	return filepath.Join(dir, "sessions", hex.EncodeToString(h[:])+".json")
}
func (e *engine) save(c *capture) error { return session.Write(recordPath(e.dir, c.Session), c.Record) }
func newEngine(dir string, push consumer.Traces, queries http.Handler) (*engine, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0700); err != nil {
		return nil, err
	}
	e := &engine{dir: dir, captures: map[string]*capture{}, local: make(chan batch, 64), queries: queries, last: time.Now()}
	remote, err := remoteFromEnv()
	e.remoteError = err != nil
	if remote != nil {
		e.remote = make(chan batch, 64)
	}
	if err := e.recoverRecords(); err != nil {
		return nil, err
	}
	e.worker(e.local, "local", push.ConsumeTraces)
	if remote != nil {
		e.worker(e.remote, "remote", remote.push)
	}
	return e, nil
}

func (e *engine) markIncomplete(c *capture) {
	c.Incomplete = true
	if c.Local == nil {
		c.Local = map[string]string{}
	}
	c.Local[c.Program], c.Local[c.Debugger] = failed, failed
	if e.remote != nil || e.remoteError || len(c.Remote) > 0 {
		if c.Remote == nil {
			c.Remote = map[string]string{}
		}
		c.Remote[c.Program], c.Remote[c.Debugger] = failed, failed
	}
}

func (e *engine) interruptCapture(c *capture) {
	if c.Closed {
		return
	}
	e.markIncomplete(c)
	c.Interrupted = true
	e.submit(c, c.batch(c.event(Event{Kind: "close"})))
	_ = e.save(c)
}

// No active producer state survives a core restart. Normalize the disk records
// before serving lists, even if no producer ever reconnects to an abandoned run.
func (e *engine) recoverRecords() error {
	paths, err := os.ReadDir(filepath.Join(e.dir, "sessions"))
	if err != nil {
		return err
	}
	for _, entry := range paths {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.dir, "sessions", entry.Name()))
		if err != nil {
			return err
		}
		var record Record
		if json.Unmarshal(data, &record) != nil || record.Closed {
			continue
		}
		c := &capture{Record: record}
		e.markIncomplete(c)
		c.Closed = true
		c.Interrupted = true
		if err := e.save(c); err != nil {
			return err
		}
	}
	return nil
}
func (e *engine) worker(queue chan batch, destination string, push func(context.Context, ptrace.Traces) error) {
	e.workers.Add(1)
	go func() {
		defer e.workers.Done()
		for b := range queue {
			if b.barrier != nil {
				close(b.barrier)
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := push(ctx, b.traces)
			cancel()
			e.mu.Lock()
			c := e.captures[b.session]
			if c != nil {
				statuses := c.Local
				if destination == "remote" {
					statuses = c.Remote
				}
				for i := 0; i < b.traces.ResourceSpans().Len(); i++ {
					rs := b.traces.ResourceSpans().At(i)
					for j := 0; j < rs.ScopeSpans().Len(); j++ {
						ss := rs.ScopeSpans().At(j)
						for k := 0; k < ss.Spans().Len(); k++ {
							id := ss.Spans().At(k).TraceID().String()
							if err != nil || statuses[id] == failed {
								statuses[id] = failed
							} else {
								statuses[id] = accepted
							}
						}
					}
				}
				if err := e.save(c); err != nil {
					c.Local[c.Program] = failed
					c.Local[c.Debugger] = failed
				}
			}
			e.mu.Unlock()
		}
	}()
}
func (e *engine) submit(c *capture, spans ptrace.Traces) {
	for i := 0; i < spans.ResourceSpans().Len(); i++ {
		rs := spans.ResourceSpans().At(i)
		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			ss := rs.ScopeSpans().At(j)
			for k := 0; k < ss.Spans().Len(); k++ {
				if ss.Spans().At(k).TraceID() == traceID(c.Program) {
					c.ProgramSpans++
				} else {
					c.DebuggerSpans++
				}
			}
		}
	}
	if spans.SpanCount() == 0 {
		return
	}
	send := func(q chan batch, status map[string]string) {
		select {
		case q <- batch{session: c.Session, traces: spans}:
		default:
			status[c.Program] = failed
			status[c.Debugger] = failed
		}
	}
	if e.remote != nil {
		copy := ptrace.NewTraces()
		spans.CopyTo(copy)
		select {
		case e.remote <- batch{session: c.Session, traces: copy}:
		default:
			c.Remote[c.Program] = failed
			c.Remote[c.Debugger] = failed
		}
	} else if e.remoteError {
		c.Remote[c.Program] = failed
		c.Remote[c.Debugger] = failed
	}
	send(e.local, c.Local)
}
func (e *engine) load(id string) (*capture, error) {
	if c := e.captures[id]; c != nil {
		return c, nil
	}
	data, err := os.ReadFile(recordPath(e.dir, id))
	if err != nil {
		return nil, err
	}
	var r Record
	if err = json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Local == nil {
		r.Local = map[string]string{}
	}
	if r.Remote == nil {
		r.Remote = map[string]string{}
	}
	c := &capture{Record: r, requests: map[int]*pending{}, threads: map[int]ptrace.Span{}, sequence: r.Sequence}
	if !c.Closed {
		e.markIncomplete(c)
	}
	e.captures[id] = c
	return c, nil
}
func (e *engine) event(v Event) (Record, error) {
	if err := validSession(v.Session); err != nil {
		return Record{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.last = time.Now()
	c, err := e.load(v.Session)
	if os.IsNotExist(err) && v.Kind == "start" {
		if len(e.captures) >= 1024 {
			return Record{}, errors.New("trace session capacity reached")
		}
		c = newCapture(v)
		e.captures[v.Session] = c
		err = nil
	}
	if err != nil {
		return Record{}, fmt.Errorf("trace session unavailable; start it first")
	}
	// Existing producers reconnect with heartbeats or observations, not another
	// start event. Resume only interrupted captures; finalized ones stay closed.
	resume := c.Interrupted && v.Kind != "close" && v.Kind != "flush"
	if c.Closed && (v.Kind == "start" || resume) {
		c.Closed = false
		c.Interrupted = false
		c.Root = newID(8)
		c.Run = newID(8)
		c.Started = v.At
		if c.Started.IsZero() {
			c.Started = time.Now()
		}
		c.requests = map[int]*pending{}
		c.threads = map[int]ptrace.Span{}
	}
	c.LastSeen = time.Now()
	if v.Kind == "close" {
		c.Interrupted = false
	}
	if v.Incomplete || c.Incomplete {
		e.markIncomplete(c)
	}
	if v.Kind != "start" && v.Kind != "flush" {
		e.submit(c, c.batch(c.event(v)))
	}
	if err = e.save(c); err != nil {
		return Record{}, errors.New("trace metadata could not be saved")
	}
	return cloneRecord(c.Record), nil
}
func cloneRecord(r Record) Record {
	data, _ := json.Marshal(r)
	var out Record
	_ = json.Unmarshal(data, &out)
	return out
}
func (e *engine) flush(ctx context.Context) error {
	for _, q := range []chan batch{e.local, e.remote} {
		if q == nil {
			continue
		}
		done := make(chan struct{})
		select {
		case q <- batch{barrier: done}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (e *engine) query(ctx context.Context, id string) (json.RawMessage, error) {
	if !idPattern.MatchString(id) {
		return nil, errors.New("invalid trace ID")
	}
	request := httptest.NewRequest("GET", "/querier/api/traces/"+id+"?mode=blocks", nil).WithContext(ctx)
	request.Header.Set("Accept", "application/protobuf")
	w := httptest.NewRecorder()
	e.queries.ServeHTTP(w, request)
	if w.Code != 200 {
		return nil, errors.New("trace is not queryable yet, or was not stored")
	}
	var trace tempopb.TraceByIDResponse
	if trace.Unmarshal(w.Body.Bytes()) != nil || trace.Trace == nil {
		return nil, errors.New("invalid trace response")
	}
	// Stock live-store queries alias its pending batches; use backend-only reads.
	// A stored fragment is not a complete capture while later batches are flushing.
	count := 0
	for _, rs := range trace.Trace.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			count += len(ss.Spans)
		}
	}
	e.mu.Lock()
	expected := 0
	for _, c := range e.captures {
		if c.Program == id {
			expected = c.ProgramSpans
		}
		if c.Debugger == id {
			expected = c.DebuggerSpans
		}
	}
	e.mu.Unlock()
	if expected == 0 {
		paths, _ := filepath.Glob(filepath.Join(e.dir, "sessions", "*.json"))
		for _, path := range paths {
			data, err := os.ReadFile(path)
			var record Record
			if err != nil || json.Unmarshal(data, &record) != nil {
				continue
			}
			if record.Program == id {
				expected = record.ProgramSpans
				break
			}
			if record.Debugger == id {
				expected = record.DebuggerSpans
				break
			}
		}
	}
	if count < expected {
		return nil, errors.New("trace export is still flushing to local blocks, or is incomplete; try again")
	}
	return tempopb.MarshalToJSONV1(trace.Trace)
}
func (e *engine) handler(origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		write := func(code int, v any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(v) }
		if "http://"+r.Host != origin || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
			write(403, map[string]string{"error": "foreign origin rejected"})
			return
		}
		e.mu.Lock()
		e.last = time.Now()
		e.mu.Unlock()
		var value any
		var err error
		switch {
		case r.Method == "GET" && r.URL.Path == "/health":
			value = map[string]any{"service": "brote-tracing", "version": 1, "sharedSpans": true}
		case r.Method == "POST" && r.URL.Path == "/api/trace-spans":
			value, err = e.spanRequest(w, r)
		case r.Method == "POST" && r.URL.Path == "/api/trace-events":
			media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if media != "application/json" {
				write(415, map[string]string{"error": "JSON required"})
				return
			}
			var v Event
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&v) != nil {
				write(400, map[string]string{"error": "invalid trace event"})
				return
			}
			value, err = e.event(v)
			if err == nil && (v.Kind == "close" || v.Kind == "flush") {
				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				err = e.flush(ctx)
				cancel()
				if err == nil {
					e.mu.Lock()
					value = cloneRecord(e.captures[v.Session].Record)
					e.mu.Unlock()
				}
			}
		case r.Method == "GET" && r.URL.Path == "/api/traces":
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			value, err = e.query(ctx, r.URL.Query().Get("id"))
			cancel()
		case r.Method == "GET" && r.URL.Path == "/api/trace-sessions":
			e.mu.Lock()
			var records []Record
			paths, _ := filepath.Glob(filepath.Join(e.dir, "sessions", "*.json"))
			for _, p := range paths {
				var record Record
				data, readErr := os.ReadFile(p)
				if readErr == nil && json.Unmarshal(data, &record) == nil {
					records = append(records, record)
				}
			}
			e.mu.Unlock()
			value = records
		default:
			write(404, map[string]string{"error": "unknown tracing endpoint"})
			return
		}
		if err != nil {
			write(409, map[string]string{"error": err.Error()})
			return
		}
		write(200, value)
	})
}
func dataDir() (string, error) {
	root, err := session.DataRoot()
	return filepath.Join(root, "tracing"), err
}

// Serve owns one shared Tempo instance. Its lock also serializes concurrent launches.
func Serve() error {
	dir, err := dataDir()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := session.Lock(dir)
	if err != nil {
		return err
	}
	defer session.Unlock(lock)
	if err = os.WriteFile(filepath.Join(dir, "service.pid"), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(dir, "service.pid"))
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	var serveErr error
	err = embeddedtempo.RunService(filepath.Join(dir, "tempo"), func(push consumer.Traces, queries http.Handler) {
		e, err := newEngine(dir, push, queries)
		if err != nil {
			serveErr = err
			return
		}
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			serveErr = err
			return
		}
		origin := "http://" + ln.Addr().String()
		server := &http.Server{Handler: e.handler(origin), ReadHeaderTimeout: 5 * time.Second}
		defer server.Close()
		if err = session.Write(filepath.Join(dir, "endpoint.json"), origin); err != nil {
			ln.Close()
			serveErr = err
			return
		}
		defer os.Remove(filepath.Join(dir, "endpoint.json"))
		done := make(chan error, 1)
		go func() { done <- server.Serve(ln) }()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
	loop:
		for {
			select {
			case <-signals:
				break loop
			case err := <-done:
				serveErr = err
				break loop
			case <-ticker.C:
				e.mu.Lock()
				idle := time.Since(e.last) > time.Minute
				active := false
				for _, c := range e.captures {
					if !c.Closed && time.Since(c.LastSeen) > 90*time.Second {
						e.interruptCapture(c)
					}
					if !c.Closed {
						active = true
					}
				}
				e.mu.Unlock()
				if idle && !active {
					break loop
				}
			}
		}
		// The process deadline also bounds upstream shutdown if a stock module stalls.
		time.AfterFunc(15*time.Second, func() { os.Exit(1) })
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		e.mu.Lock()
		for _, c := range e.captures {
			e.interruptCapture(c)
		}
		e.mu.Unlock()
		_ = e.flush(ctx)
		close(e.local)
		if e.remote != nil {
			close(e.remote)
		}
		e.workers.Wait()
	})
	if err != nil {
		return err
	}
	return serveErr
}
