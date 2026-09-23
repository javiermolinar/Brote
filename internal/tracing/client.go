package tracing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var localHTTP = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func validEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}
func probe(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "endpoint.json"))
	var endpoint string
	if err != nil || json.Unmarshal(data, &endpoint) != nil || !validEndpoint(endpoint) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+"/health", nil)
	resp, err := localHTTP.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var status struct {
		Service string
		Version int
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&status) != nil || status.Service != "brote-tracing" || status.Version != 1 {
		return ""
	}
	return endpoint
}

// Ensure connects to or starts the shared Go service; callers do not own Tempo.
func Ensure(ctx context.Context) (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if endpoint := probe(dir); endpoint != "" {
		return endpoint, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(exe, ".test") {
		return "", errors.New("tracing requires the Brote executable")
	}
	log, err := os.OpenFile(filepath.Join(dir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer log.Close()
	cmd := exec.Command(exe, "trace-serve")
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return "", err
	}
	go func() { _ = cmd.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if endpoint := probe(dir); endpoint != "" {
			return endpoint, nil
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return "", errors.New("core tracing startup timed out")
		case <-ticker.C:
		}
	}
}
func Request(ctx context.Context, endpoint, route string, body any, out any) error {
	if !validEndpoint(endpoint) {
		return errors.New("invalid core endpoint")
	}
	method := "GET"
	var data []byte
	var err error
	if body != nil {
		method = "POST"
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint+"/api/"+route, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := localHTTP.Do(req)
	if err != nil {
		return errors.New("core tracing unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var failure struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&failure) == nil && failure.Error != "" {
			return errors.New(failure.Error)
		}
		return errors.New("core tracing request failed")
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 128<<20)).Decode(out)
}

// Recorder is a bounded, nonblocking broker producer. All schema/export work runs
// in the core service, shared by CLI, Codex, Pi and native editor sessions.
type Recorder struct {
	mu      sync.Mutex
	events  chan Event
	done    chan struct{}
	closed  bool
	record  Record
	failure string
	session string
}

func NewRecorder(id, name, adapter string) *Recorder {
	r := &Recorder{events: make(chan Event, 256), done: make(chan struct{}), session: id}
	r.events <- Event{Session: id, Name: name, Adapter: adapter, Kind: "start", At: time.Now()}
	go r.run()
	return r
}
func (r *Recorder) run() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	endpoint, err := Ensure(ctx)
	cancel()
	r.runEvents(endpoint, err)
}
func (r *Recorder) runEvents(endpoint string, err error) {
	defer close(r.done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		var event Event
		select {
		case v, ok := <-r.events:
			if !ok {
				return
			}
			event = v
		case <-ticker.C:
			event = Event{Session: r.session, Kind: "heartbeat"}
		}
		if err != nil {
			r.mu.Lock()
			r.failure = failed
			r.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var record Record
		r.mu.Lock()
		event.Incomplete = event.Incomplete || r.failure != ""
		r.mu.Unlock()
		requestErr := Request(ctx, endpoint, "trace-events", event, &record)
		cancel()
		r.mu.Lock()
		if requestErr != nil {
			r.failure = failed
		} else {
			r.record = record
		}
		r.mu.Unlock()
		if requestErr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			endpoint, err = Ensure(ctx)
			cancel()
		}
	}
}
func (r *Recorder) Event(e Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	e.Session = r.session
	if e.At.IsZero() {
		e.At = time.Now()
	}
	select {
	case r.events <- e:
	default:
		r.failure = failed
	}
}
func (r *Recorder) Status() any {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return map[string]any{"record": cloneRecord(r.record), "error": r.failure}
}
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	r.mu.Unlock()
	// A broker can end while the service is still in its 30-second startup window.
	// Use one budget for startup, queued observations and the final flush.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	select {
	case r.events <- Event{Session: r.session, Kind: "close", At: time.Now()}:
	case <-ctx.Done():
	}
	close(r.events)
	select {
	case <-r.done:
	case <-ctx.Done():
	}
}

// Records and Query also work after a debug broker has exited.
func Records(ctx context.Context) ([]Record, error) {
	endpoint, err := Ensure(ctx)
	if err != nil {
		return nil, err
	}
	var out []Record
	err = Request(ctx, endpoint, "trace-sessions", nil, &out)
	return out, err
}
func Query(ctx context.Context, id string) (json.RawMessage, error) {
	if !idPattern.MatchString(id) {
		return nil, errors.New("invalid trace ID")
	}
	endpoint, err := Ensure(ctx)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = Request(ctx, endpoint, "traces?id="+id, nil, &out)
	return out, err
}
