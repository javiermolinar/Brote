// Package embeddedtempo contains Brote's process-scoped Tempo compatibility boundary.
package embeddedtempo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/grafana/tempo/v3/cmd/tempo/app"
	"github.com/grafana/tempo/v3/modules/distributor/receiver"
	"github.com/grafana/tempo/v3/pkg/gogocodec"
	"github.com/grafana/tempo/v3/pkg/tempopb"
	"github.com/grafana/tempo/v3/pkg/util/log"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.yaml.in/yaml/v2"
	"google.golang.org/grpc/encoding"
)

// Run holds an exclusive data lock until exit. Only the owner's inherited pipes
// accept Brote pushes and queries; Tempo internal listeners bind only to loopback.
func Run(dataDir string, input io.Reader, output io.Writer) error {
	return RunService(dataDir, func(push consumer.Traces, queries http.Handler) {
		encoder := json.NewEncoder(output)
		if encoder.Encode(map[string]interface{}{"ready": true, "dataDir": dataDir}) == nil {
			serveRequests(input, encoder, push, queries)
		}
	})
}

// RunService gives the Go core direct access to ingestion and the stock querier.
// Returning from serve initiates Tempo shutdown. Run once per process.
func RunService(dataDir string, serve func(consumer.Traces, http.Handler)) error {
	if dataDir == "" {
		return errors.New("trace data directory is required")
	}
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "runtime.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("trace storage is already open in another Brote runtime")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	cfg, err := configuration(dir)
	if err != nil {
		return err
	}
	server, err := newLoopbackServer()
	if err != nil {
		return err
	}
	defer server.listener.Close()
	cfg.Server.GRPCListenPort = server.port()
	cfg.Querier.Worker.FrontendAddress = server.listener.Addr().String()
	cfg.BackendWorker.BackendSchedulerAddr = server.listener.Addr().String()
	encoding.RegisterCodec(gogocodec.NewCodec())
	log.InitLogger(&cfg.Server)
	t, err := app.New(*cfg)
	if err != nil {
		return err
	}
	t.Server = server
	t.ModuleManager.RegisterModule(app.MetricsGenerator, nil)
	target := make(chan consumer.Traces, 1)
	original := t.TracesConsumerMiddleware
	t.TracesConsumerMiddleware = receiver.MiddlewareFunc(func(next consumer.Traces) consumer.Traces {
		wrapped := original.Wrap(next)
		target <- wrapped
		return wrapped
	})
	done := make(chan struct{})
	defer close(done)
	go func() {
		var push consumer.Traces
		select {
		case push = <-target:
		case <-done:
			return
		}
		select {
		case <-server.running:
		case <-done:
			return
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				response := httptest.NewRecorder()
				server.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
				if response.Code != http.StatusOK {
					continue
				}
				serve(push, server.HTTPHandler())
				t.Stop()
				return
			}
		}
	}()
	return t.Run()
}

type request struct {
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Data    []byte `json:"data"`
	TraceID string `json:"traceID"`
}
type response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

var traceIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func serveRequests(input io.Reader, encoder *json.Encoder, push consumer.Traces, queries http.Handler) {
	scanner := bufio.NewScanner(input)
	// A 16 MiB protobuf payload expands by 4/3 in the pipe's JSON envelope.
	scanner.Buffer(make([]byte, 64<<10), 24<<20)
	for scanner.Scan() {
		var req request
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			return
		}
		reply := response{ID: req.ID}
		switch req.Method {
		case "push":
			if len(req.Data) > 16<<20 {
				reply.Error = "Trace payload exceeds 16 MiB."
				break
			}
			traces, err := (&ptrace.ProtoUnmarshaler{}).UnmarshalTraces(req.Data)
			if err != nil {
				reply.Error = "Invalid trace payload."
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err = push.ConsumeTraces(ctx, traces)
			cancel()
			if err != nil {
				reply.Error = "Local trace ingestion failed."
			}
		case "query":
			if !traceIDPattern.MatchString(req.TraceID) {
				reply.Error = "Invalid trace ID."
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			// Use the existing direct querier route so trace-ID reads do not
			// populate frontend tenant queues that hang during upstream shutdown.
			// Backend-only mode also avoids the stock live-store combiner mutating
			// pending batches during a read. Newly exported spans must flush first.
			query := httptest.NewRequest(http.MethodGet, "/querier/api/traces/"+req.TraceID+"?mode=blocks", nil).WithContext(ctx)
			query.Header.Set("Accept", "application/protobuf")
			result := httptest.NewRecorder()
			queries.ServeHTTP(result, query)
			cancel()
			switch result.Code {
			case http.StatusOK:
				// Use Tempo's own v1 marshaller to preserve its public JSON shape.
				var body tempopb.TraceByIDResponse
				if err := body.Unmarshal(result.Body.Bytes()); err != nil || body.Trace == nil {
					reply.Error = "Local trace query returned an invalid response."
				} else {
					data, err := tempopb.MarshalToJSONV1(body.Trace)
					if err != nil {
						reply.Error = "Local trace query encoding failed."
					} else {
						reply.Result = data
					}
				}
			case http.StatusNotFound:
				reply.Error = "Trace is not queryable yet, or was not stored. Try again after export completes."
			default:
				reply.Error = "Local trace query failed."
			}
		default:
			reply.Error = "Unknown runtime request."
		}
		if encoder.Encode(reply) != nil {
			return
		}
	}
}

func configuration(dir string) (*app.Config, error) {
	cfg := app.NewDefaultConfig()
	if err := yaml.UnmarshalStrict([]byte(profile), cfg); err != nil {
		return nil, err
	}
	cfg.LiveStore.Ring.KVStore.Store = "inmemory"
	cfg.LiveStore.PartitionRing.KVStore.Store = "inmemory"
	cfg.Distributor.DistributorRing.KVStore.Store = "inmemory"
	// Run replaces these internal addresses with its already-bound loopback port.
	cfg.Server.GRPCListenPort = 1
	cfg.Querier.Worker.FrontendAddress = "127.0.0.1:1"
	cfg.BackendWorker.BackendSchedulerAddr = "127.0.0.1:1"
	cfg.LiveStore.WAL.Filepath = filepath.Join(dir, "live-wal")
	cfg.LiveStore.ShutdownMarkerDir = filepath.Join(dir, "shutdown")
	cfg.StorageConfig.Trace.WAL.Filepath = filepath.Join(dir, "wal")
	cfg.StorageConfig.Trace.Local.Path = filepath.Join(dir, "blocks")
	cfg.BackendScheduler.LocalWorkPath = filepath.Join(dir, "scheduler")
	// Stock Tempo has no infinite-retention switch. Use the maximum finite
	// duration (about 292 years), while keeping obsolete-block cleanup enabled.
	cfg.BackendWorker.Compactor.BlockRetention = time.Duration(1<<63 - 1)
	const queryMessageBytes = 128 << 20
	cfg.Server.GRPCServerMaxRecvMsgSize = queryMessageBytes
	cfg.Server.GRPCServerMaxSendMsgSize = queryMessageBytes
	cfg.Querier.Worker.GRPCClientConfig.MaxRecvMsgSize = queryMessageBytes
	cfg.Querier.Worker.GRPCClientConfig.MaxSendMsgSize = queryMessageBytes
	cfg.LiveStoreClient.GRPCClientConfig.MaxRecvMsgSize = queryMessageBytes
	cfg.LiveStoreClient.GRPCClientConfig.MaxSendMsgSize = queryMessageBytes
	return cfg, nil
}

const profile = `stream_over_http_enabled: false
server:
  http_listen_address: 127.0.0.1
  grpc_listen_address: 127.0.0.1
  log_level: warn
memberlist:
  bind_addr: [127.0.0.1]
querier:
  max_concurrent_queries: 2
query_frontend:
  query_end_cutoff: 1s
  search:
    query_backend_after: 45s
    concurrent_jobs: 2
  trace_by_id:
    concurrent_shards: 2
distributor:
  receivers:
    otlp:
      protocols:
        grpc:
          endpoint: 127.0.0.1:0
  max_attribute_bytes: 65536
live_store:
  max_block_duration: 10s
  complete_block_timeout: 60s
  flush_op_timeout: 2s
  block_reclaim_grace: 5s
  query_block_concurrency: 2
  complete_block_concurrency: 1
  ring:
    instance_addr: 127.0.0.1
storage:
  trace:
    blocklist_poll: 2s
    search:
      read_buffer_size_bytes: 131072
    pool:
      max_workers: 4
      queue_depth: 100
    backend: local
    local:
      path: data/blocks
backend_worker:
  finish_on_shutdown_timeout: 10s
  compaction:
    retention_concurrency: 1
    retention_block_concurrency: 1
usage_report:
  reporting_enabled: false
overrides:
  defaults:
    global:
      max_bytes_per_trace: 0
`
