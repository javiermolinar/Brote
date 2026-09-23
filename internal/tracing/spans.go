package tracing

// Shared-session spans retain their run/capture IDs and use the same local and
// remote queues as native adapter observations. The broker never exports twice.
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	sdk "go.opentelemetry.io/otel/sdk/trace"
)

type SpanBatch struct {
	Record Record `json:"record"`
	Data   []byte `json:"data,omitempty"`
	Close  bool   `json:"close,omitempty"`
}

func (e *engine) spanBatch(v SpanBatch) (Record, error) {
	r := v.Record
	if validSession(r.Session) != nil || !idPattern.MatchString(r.Program) || !idPattern.MatchString(r.Debugger) || r.Program == r.Debugger {
		return Record{}, errors.New("invalid shared trace identity")
	}
	spans := ptrace.NewTraces()
	var err error
	if len(v.Data) > 0 {
		spans, err = (&ptrace.ProtoUnmarshaler{}).UnmarshalTraces(v.Data)
		if err != nil || spans.SpanCount() > 64 {
			return Record{}, errors.New("invalid shared trace batch")
		}
	}
	for i := 0; i < spans.ResourceSpans().Len(); i++ {
		rs := spans.ResourceSpans().At(i)
		for j := 0; j < rs.ScopeSpans().Len(); j++ {
			ss := rs.ScopeSpans().At(j)
			for k := 0; k < ss.Spans().Len(); k++ {
				id := ss.Spans().At(k).TraceID().String()
				if id != r.Program && id != r.Debugger {
					return Record{}, errors.New("shared trace identity mismatch")
				}
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.last = time.Now()
	c, err := e.load(r.Session)
	if os.IsNotExist(err) {
		if len(e.captures) >= 1024 {
			return Record{}, errors.New("trace session capacity reached")
		}
		c = &capture{Record: Record{Session: r.Session, Name: bounded(r.Name, 512), Adapter: "brote", Program: r.Program, Debugger: r.Debugger, Run: r.Run, Root: r.Root, Started: r.Started, External: true, Local: map[string]string{}, Remote: map[string]string{}}}
		e.captures[r.Session] = c
		err = nil
	}
	if err != nil {
		return Record{}, err
	}
	if !c.External || c.Program != r.Program || c.Debugger != r.Debugger {
		return Record{}, errors.New("shared trace identity mismatch")
	}
	if c.Closed && !c.Interrupted {
		if v.Close && spans.SpanCount() == 0 {
			return cloneRecord(c.Record), nil
		}
		return Record{}, errors.New("trace session is closed")
	}
	c.Closed = v.Close
	c.Interrupted = false
	c.LastSeen = time.Now()
	if r.Incomplete {
		e.markIncomplete(c)
	}
	e.submit(c, spans)
	if err = e.save(c); err != nil {
		return Record{}, errors.New("trace metadata could not be saved")
	}
	return cloneRecord(c.Record), nil
}

// SpanExporter transports SDK spans to the shared core. Only that service owns
// Tempo and the remote OTLP exporter; no remote credentials cross this API.
type SpanExporter struct {
	incomplete bool
	mu         sync.Mutex
	Endpoint   string
	Record     Record
	stop       chan struct{}
	once       sync.Once
}

func NewSpanExporter(endpoint string) *SpanExporter {
	return &SpanExporter{Endpoint: endpoint, stop: make(chan struct{})}
}
func (x *SpanExporter) Start() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = x.send(ctx, SpanBatch{Record: x.Record})
		cancel()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-x.stop:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = x.send(ctx, SpanBatch{Record: x.Record})
				cancel()
			}
		}
	}()
}
func (x *SpanExporter) send(ctx context.Context, v SpanBatch) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.Endpoint == "" {
		endpoint, err := Ensure(ctx)
		if err != nil {
			x.incomplete = true
			return err
		}
		x.Endpoint = endpoint
	}
	v.Record.Incomplete = v.Record.Incomplete || x.incomplete
	var record Record
	if err := Request(ctx, x.Endpoint, "trace-spans", v, &record); err != nil {
		x.Endpoint = ""
		x.incomplete = true
		return err
	}
	for _, status := range record.Local {
		if status == failed {
			return errors.New("local trace export failed or incomplete")
		}
	}
	for _, status := range record.Remote {
		if status == failed {
			return errors.New("remote trace export failed or incomplete")
		}
	}
	return nil
}
func (x *SpanExporter) ExportSpans(ctx context.Context, spans []sdk.ReadOnlySpan) error {
	data := ptrace.NewTraces()
	for _, s := range spans {
		rs := data.ResourceSpans().AppendEmpty()
		for _, a := range s.Resource().Attributes() {
			v := rs.Resource().Attributes().PutEmpty(string(a.Key))
			_ = v.FromRaw(a.Value.AsInterface())
		}
		ss := rs.ScopeSpans().AppendEmpty()
		ss.Scope().SetName(s.InstrumentationScope().Name)
		p := ss.Spans().AppendEmpty()
		p.SetName(s.Name())
		p.SetTraceID(pcommon.TraceID(s.SpanContext().TraceID()))
		p.SetSpanID(pcommon.SpanID(s.SpanContext().SpanID()))
		p.SetParentSpanID(pcommon.SpanID(s.Parent().SpanID()))
		p.SetStartTimestamp(pcommon.NewTimestampFromTime(s.StartTime()))
		p.SetEndTimestamp(pcommon.NewTimestampFromTime(s.EndTime()))
		for _, a := range s.Attributes() {
			v := p.Attributes().PutEmpty(string(a.Key))
			_ = v.FromRaw(a.Value.AsInterface())
		}
		for _, link := range s.Links() {
			l := p.Links().AppendEmpty()
			l.SetTraceID(pcommon.TraceID(link.SpanContext.TraceID()))
			l.SetSpanID(pcommon.SpanID(link.SpanContext.SpanID()))
		}
	}
	payload, err := (&ptrace.ProtoMarshaler{}).MarshalTraces(data)
	if err != nil {
		return err
	}
	return x.send(ctx, SpanBatch{Record: x.Record, Data: payload})
}
func (x *SpanExporter) Shutdown(ctx context.Context) error {
	x.once.Do(func() { close(x.stop) })
	return x.send(ctx, SpanBatch{Record: x.Record, Close: true})
}

func (e *engine) spanRequest(w http.ResponseWriter, r *http.Request) (any, error) {
	var v SpanBatch
	if err := decodeSpanBatch(w, r, &v); err != nil {
		return nil, err
	}
	record, err := e.spanBatch(v)
	if err != nil {
		return nil, err
	}
	if len(v.Data) > 0 || v.Close {
		if err = e.flush(r.Context()); err != nil {
			return nil, err
		}
		e.mu.Lock()
		record = cloneRecord(e.captures[v.Record.Session].Record)
		e.mu.Unlock()
	}
	return record, nil
}

func decodeSpanBatch(w http.ResponseWriter, r *http.Request, v *SpanBatch) error {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		return errors.New("JSON required")
	}
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v)
}

func EnsureSpanService(ctx context.Context) (string, error) {
	endpoint, err := Ensure(ctx)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/health", nil)
	if err != nil {
		return "", err
	}
	resp, err := localHTTP.Do(req)
	if err != nil {
		return "", errors.New("core tracing unavailable")
	}
	defer resp.Body.Close()
	var info struct {
		SharedSpans bool `json:"sharedSpans"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&info) != nil || !info.SharedSpans {
		return "", errors.New("running trace service lacks shared-span support; restart the tracing service before launching this session")
	}
	return endpoint, nil
}
