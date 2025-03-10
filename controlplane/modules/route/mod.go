package route

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/controlplane/internal/ffi"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/rib"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/routepb"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// RouteModule is a controlplane part of a module that is responsible for
// routing configuration.
type RouteModule struct {
	cfg                *Config
	server             *grpc.Server
	agents             []*ffi.Agent
	neighbourDiscovery *neigh.NeighMonitor
	log                *zap.SugaredLogger
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(cfg *Config, log *zap.SugaredLogger) (*RouteModule, error) {
	log = log.With(zap.String("module", "routepb.Route"))

	neighbourCache := discovery.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	neighbourDiscovery := neigh.NewNeighMonitor(neighbourCache, neigh.WithLog(log))

	rib := rib.NewRIB(neighbourCache, log)

	// TODO: obtain NUMA topology.
	numaIndices := []int{0}

	agents := make([]*ffi.Agent, 0)
	for numaIdx := range numaIndices {
		path := fmt.Sprintf("%s%d", cfg.MemoryPathPrefix, numaIdx)
		log.Debugw("mapping shared memory",
			zap.Int("numa", numaIdx),
			zap.Uint("size", cfg.MemoryRequirements),
			zap.String("path", path),
		)

		agent, err := ffi.NewAgent(path, "route", cfg.MemoryRequirements)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to shared memory on NUMA %d: %w", numaIdx, err)
		}

		agents = append(agents, agent)
	}

	server := grpc.NewServer()

	routeService := NewRouteService(agents, rib, log)
	routepb.RegisterRouteServer(server, routeService)

	neighbourService := NewNeighbourService(neighbourCache, log)
	routepb.RegisterNeighbourServer(server, neighbourService)

	return &RouteModule{
		cfg:                cfg,
		server:             server,
		agents:             agents,
		neighbourDiscovery: neighbourDiscovery,
		log:                log,
	}, nil
}

// Close closes the module.
func (m *RouteModule) Close() error {
	for numaIdx, agent := range m.agents {
		if err := agent.Close(); err != nil {
			m.log.Warnw("failed to close shared memory mapping", zap.Int("numa", numaIdx), zap.Error(err))
		}
	}

	return nil
}

// Run runs the module until the specified context is canceled.
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
	wg.Go(func() error {
		return m.neighbourDiscovery.Run(ctx)
	})

	if err := m.registerServices(ctx, listenerAddr); err != nil {
		return fmt.Errorf("failed to register services: %w", err)
	}

	<-ctx.Done()

	m.log.Infow("stopping gRPC API", zap.Stringer("addr", listenerAddr))
	defer m.log.Infow("stopped gRPC API", zap.Stringer("addr", listenerAddr))

	m.server.GracefulStop()

	return wg.Wait()
}

func (m *RouteModule) registerServices(ctx context.Context, listenerAddr net.Addr) error {
	gatewayConn, err := grpc.NewClient(
		m.cfg.GatewayEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize gateway gRPC client: %w", err)
	}

	gateway := ynpb.NewGatewayClient(gatewayConn)

	servicesNames := []string{
		"routepb.Route",
		"routepb.Neighbour",
	}

	wg, ctx := errgroup.WithContext(ctx)
	for _, serviceName := range servicesNames {
		serviceName := serviceName
		req := &ynpb.RegisterRequest{
			Name:     serviceName,
			Endpoint: listenerAddr.String(),
		}

		wg.Go(func() error {
			for {
				if _, err := gateway.Register(ctx, req); err == nil {
					m.log.Infof("successfully registered %q in the Gateway API", serviceName)
					return nil
				}

				m.log.Warnf("failed to register %q in the Gateway API: %v", serviceName, err)
				// TODO: exponential backoff should fit better here.
				time.Sleep(1 * time.Second)
			}
		})
	}

	return wg.Wait()
}
