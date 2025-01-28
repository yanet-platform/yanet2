package route

import (
	"context"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/controlplane/modules/route/routepb"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

const moduleName = "routepb.Route"

type RouteModule struct {
	cfg    *Config
	server *grpc.Server
	log    *zap.SugaredLogger
}

func NewRouteModule(cfg *Config, log *zap.SugaredLogger) *RouteModule {
	log = log.With(zap.String("module", moduleName))

	server := grpc.NewServer()
	service := NewRouteService(log)
	routepb.RegisterRouteServer(server, service)

	return &RouteModule{
		cfg:    cfg,
		server: server,
		log:    log,
	}
}

func (m *RouteModule) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", m.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	listenerAddr := listener.Addr()
	m.log.Infow("exposing gRPC API", zap.Stringer("addr", listenerAddr))

	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.server.Serve(listener)
	})

	gatewayConn, err := grpc.NewClient(
		m.cfg.GatewayEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize gateway gRPC client: %w", err)
	}

	gateway := ynpb.NewGatewayClient(gatewayConn)

	req := &ynpb.RegisterRequest{
		Name:     moduleName,
		Endpoint: listenerAddr.String(),
	}

	for {
		if _, err := gateway.Register(ctx, req); err == nil {
			m.log.Infof("successfully registered in the Gateway API")
			break
		}

		m.log.Warnf("failed to register in the Gateway API: %v", err)
		time.Sleep(1 * time.Second)
	}

	<-ctx.Done()

	m.log.Infow("stopping gRPC API", zap.Stringer("addr", listenerAddr))
	defer m.log.Infow("stopped gRPC API", zap.Stringer("addr", listenerAddr))

	m.server.GracefulStop()

	return wg.Wait()
}
