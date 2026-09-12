package operator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// Largest gRPC request an operator accepts.
//
// A bulk configuration push carries a whole table in one request, so the
// four mebibytes gRPC defaults to would bound a legitimate update; the
// gateway standing in front of the operators allows the same ceiling. Only
// the request direction is capped: a reply repeats a table back with
// everything the operator stamps on each entry, so it outgrows the request
// that produced it and keeps the far larger ceiling gRPC gives it. A reply
// travelling through the gateway is still bounded by the gateway.
const maxRequestSize = 1024 * 1024 * 256

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
	server *grpc.Server
	log    *zap.Logger
}

// NewGRPCServer constructs a GRPCServer with the supplied service
// registrars applied to a fresh grpc.Server and returns registered
// service names.
func NewGRPCServer(
	cfg GRPCServerConfig,
	services []ServiceRegistrar,
	options ...GRPCServerOption,
) (*GRPCServer, []string) {
	opts := newGRPCServerOptions()
	for _, o := range options {
		o(opts)
	}

	server := grpc.NewServer(grpc.MaxRecvMsgSize(maxRequestSize))
	serviceNames := make([]string, len(services))
	for idx, register := range services {
		serviceNames[idx] = register(server)
	}

	return &GRPCServer{
		server: server,
		log:    opts.Log,
	}, serviceNames
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

		xgrpc.StopGracefully(m.server, xgrpc.GracefulStopTimeout, func() {
			m.log.Warn("graceful stop timed out, forcing shutdown",
				zap.Duration("grace_period", xgrpc.GracefulStopTimeout),
			)
		})
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
