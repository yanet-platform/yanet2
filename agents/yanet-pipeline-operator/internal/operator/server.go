package operator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

type grpcServerOptions struct {
	Log *zap.Logger
}

func newGRPCServerOptions() *grpcServerOptions {
	return &grpcServerOptions{
		Log: zap.NewNop(),
	}
}

type GRPCServerOption func(*grpcServerOptions)

func WithGRPCLog(log *zap.Logger) GRPCServerOption {
	return func(o *grpcServerOptions) {
		o.Log = log
	}
}

type GRPCServer struct {
	cfg    *operator.GRPCServerConfig
	server *grpc.Server
	log    *zap.Logger
}

func NewGRPCServer(
	cfg *operator.GRPCServerConfig,
	service *Service,
	options ...GRPCServerOption,
) *GRPCServer {
	opts := newGRPCServerOptions()
	for _, o := range options {
		o(opts)
	}

	server := grpc.NewServer()
	operatorpb.RegisterPipelineOperatorServiceServer(server, service)
	operatorpb.RegisterMetricsServiceServer(server, service)

	return &GRPCServer{
		cfg:    cfg,
		server: server,
		log:    opts.Log,
	}
}

func (m *GRPCServer) Run(ctx context.Context, listener net.Listener) error {
	serveErr := make(chan error, 1)
	go func() {
		m.log.Info("exposing gRPC server",
			zap.Stringer("addr", listener.Addr()),
		)
		serveErr <- m.server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		m.log.Info("stopping gRPC server", zap.Stringer("addr", listener.Addr()))
		defer m.log.Info("stopped gRPC server", zap.Stringer("addr", listener.Addr()))

		m.server.GracefulStop()
		// Drain Serve's return value; after GracefulStop it returns nil
		// on clean shutdown.
		if err := <-serveErr; err != nil {
			return fmt.Errorf("failed to serve gRPC: %w", err)
		}
		return nil
	case err := <-serveErr:
		// Serve returned before ctx was cancelled — treat as fatal.
		if err != nil {
			return fmt.Errorf("failed to serve gRPC: %w", err)
		}
		return nil
	}
}
