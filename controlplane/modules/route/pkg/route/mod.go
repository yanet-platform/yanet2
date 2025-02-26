package route

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yanet-platform/yanet2/controlplane/internal/pkg/ffi"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/link"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/rib"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/routepb"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

const moduleName = "routepb.Route"

// RouteModule is a controlplane part of a module that is responsible for
// routing configuration.
type RouteModule struct {
	cfg                *Config
	server             *grpc.Server
	agents             []*ffi.Agent
	linkDiscovery      *link.LinkMonitor
	neighbourDiscovery *neigh.NeighMonitor
	log                *zap.SugaredLogger
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(cfg *Config, log *zap.SugaredLogger) (*RouteModule, error) {
	log = log.With(zap.String("module", moduleName))

	linkCache := discovery.NewEmptyCache[int, netlink.LinkAttrs]()
	linkDiscovery := link.NewLinkMonitor(linkCache, link.WithLog(log))

	neighbourCache := discovery.NewEmptyCache[netip.Addr, netlink.Neigh]()
	neighbourDiscovery := neigh.NewNeighMonitor(neighbourCache, neigh.WithLog(log))

	rib := rib.NewRIB(neighbourCache, linkCache, log)

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
	service := NewRouteService(agents, rib, log)
	routepb.RegisterRouteServer(server, service)

	return &RouteModule{
		cfg:                cfg,
		server:             server,
		agents:             agents,
		linkDiscovery:      linkDiscovery,
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
		return m.linkDiscovery.Run(ctx)
	})
	wg.Go(func() error {
		return m.neighbourDiscovery.Run(ctx)
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
