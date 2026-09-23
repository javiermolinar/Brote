package embeddedtempo

import (
	"context"
	"net"
	"net/http"

	"github.com/go-kit/log"
	"github.com/gorilla/mux"
	"github.com/grafana/dskit/server"
	"github.com/grafana/dskit/services"
	"github.com/grafana/tempo/v3/cmd/tempo/app"
	util_log "github.com/grafana/tempo/v3/pkg/util/log"
	"google.golang.org/grpc"
)

// loopbackServer keeps HTTP queries in-process and binds internal RPC to loopback.
type loopbackServer struct {
	router   *mux.Router
	listener net.Listener
	rpc      *grpc.Server
	running  chan struct{}
}

var _ app.TempoServer = (*loopbackServer)(nil)

func newLoopbackServer() (*loopbackServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return &loopbackServer{router: mux.NewRouter(), listener: listener, running: make(chan struct{})}, nil
}
func (s *loopbackServer) port() int                 { return s.listener.Addr().(*net.TCPAddr).Port }
func (s *loopbackServer) HTTPRouter() *mux.Router   { return s.router }
func (s *loopbackServer) HTTPHandler() http.Handler { return s.router }
func (s *loopbackServer) GRPC() *grpc.Server        { return s.rpc }
func (s *loopbackServer) Log() log.Logger           { return util_log.Logger }
func (*loopbackServer) EnableHTTP2()                {}
func (*loopbackServer) SetKeepAlivesEnabled(bool)   {}
func (s *loopbackServer) StartAndReturnService(cfg server.Config, _ bool, waitFor func() []services.Service) (services.Service, error) {
	opts := append([]grpc.ServerOption{}, cfg.GRPCOptions...)
	opts = append(opts, grpc.MaxRecvMsgSize(cfg.GRPCServerMaxRecvMsgSize), grpc.MaxSendMsgSize(cfg.GRPCServerMaxSendMsgSize), grpc.ChainUnaryInterceptor(cfg.GRPCMiddleware...), grpc.ChainStreamInterceptor(cfg.GRPCStreamMiddleware...))
	s.rpc = grpc.NewServer(opts...)
	done := make(chan error, 1)
	return services.NewBasicService(nil, func(ctx context.Context) error {
		close(s.running)
		go func() { done <- s.rpc.Serve(s.listener) }()
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			done <- err
			return err
		}
	}, func(error) error {
		for _, service := range waitFor() {
			_ = service.AwaitTerminated(context.Background())
		}
		s.rpc.GracefulStop()
		s.listener.Close()
		<-done
		return nil
	}), nil
}
