package operator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/agents/yanet-pipeline-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
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

type GRPCServerConfig struct {
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

type GRPCServer struct {
	cfg    *GRPCServerConfig
	server *grpc.Server
	log    *zap.Logger
}

func NewGRPCServer(
	cfg *GRPCServerConfig,
	service *Service,
	options ...GRPCServerOption,
) *GRPCServer {
	opts := newGRPCServerOptions()
	for _, o := range options {
		o(opts)
	}

	server := grpc.NewServer()
	operatorpb.RegisterPipelineOperatorServiceServer(server, service)

	return &GRPCServer{
		cfg:    cfg,
		server: server,
		log:    opts.Log,
	}
}

func (m *GRPCServer) Run(ctx context.Context) error {
	endpoint := m.cfg.Endpoint.Unwrap()
	listener, err := net.Listen("tcp", endpoint)
	if err != nil {
		return fmt.Errorf("failed to listen gRPC on %q: %w", endpoint, err)
	}

	m.log.Info("exposing gRPC server",
		zap.String("addr", listener.Addr().String()),
	)

	serveErr := make(chan error, 1)
	go func() {
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
