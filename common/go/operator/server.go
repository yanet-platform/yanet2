package operator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"google.golang.org/grpc"
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

// WithGRPCLog sets the logger used by the gRPC server wrapper.
func WithGRPCLog(log *zap.Logger) GRPCServerOption {
	return func(o *grpcServerOptions) {
		o.Log = log
	}
}

// GRPCServer wraps a grpc.Server with the operator's service set.
type GRPCServer struct {
	cfg    *GRPCServerConfig
	server *grpc.Server
	log    *zap.Logger
}

// NewGRPCServer constructs a GRPCServer with the supplied registrars
// applied to a fresh grpc.Server.
func NewGRPCServer(
	cfg *GRPCServerConfig,
	registrars []func(*grpc.Server),
	options ...GRPCServerOption,
) *GRPCServer {
	opts := newGRPCServerOptions()
	for _, o := range options {
		o(opts)
	}

	server := grpc.NewServer()
	for _, register := range registrars {
		register(server)
	}

	return &GRPCServer{
		cfg:    cfg,
		server: server,
		log:    opts.Log,
	}
}

// Run serves until the supplied context is cancelled.
//
// On cancellation it performs a graceful stop and drains Serve's return value.
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
		// Drain Serve's return value.
		if err := <-serveErr; err != nil {
			return fmt.Errorf("failed to serve gRPC: %w", err)
		}
		return nil
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("failed to serve gRPC: %w", err)
		}
		return nil
	}
}
