package operator

import (
	"context"
	"fmt"
	"net"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/agents/yanet-route-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// GRPCServerConfig describes how to expose the operator's gRPC server.
type GRPCServerConfig struct {
	Endpoint xcfg.NonEmptyString `yaml:"endpoint"`
}

// GRPCServer wraps a grpc.Server with the operator's service set.
type GRPCServer struct {
	cfg    *GRPCServerConfig
	server *grpc.Server
	log    *zap.Logger
}

// NewGRPCServer registers all operator services on a fresh grpc.Server
// and returns the ready-to-run wrapper.
func NewGRPCServer(
	cfg *GRPCServerConfig,
	routeSvc *RouteService,
	neighbourSvc *NeighbourService,
	metricsSvc *MetricsService,
	operatorSvc *RouteOperatorService,
	options ...GRPCServerOption,
) *GRPCServer {
	opts := newGRPCServerOptions()
	for _, o := range options {
		o(opts)
	}

	server := grpc.NewServer()
	operatorpb.RegisterRouteServiceServer(server, routeSvc)
	operatorpb.RegisterNeighbourServiceServer(server, neighbourSvc)
	operatorpb.RegisterMetricsServiceServer(server, metricsSvc)
	operatorpb.RegisterRouteOperatorServiceServer(server, operatorSvc)

	return &GRPCServer{
		cfg:    cfg,
		server: server,
		log:    opts.Log,
	}
}

// Run serves until the supplied context is cancelled. On cancellation
// it performs a graceful stop and drains Serve's return value.
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
