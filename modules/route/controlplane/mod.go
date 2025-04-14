package route

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/yncp/gateway"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/discovery"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/discovery/bird"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/rib"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

// RouteModule is a controlplane part of a module that is responsible for
// routing configuration.
type RouteModule struct {
	cfg                *Config
	server             *grpc.Server
	shm                *ffi.SharedMemory
	agents             []*ffi.Agent
	neighbourDiscovery *neigh.NeighMonitor
	birdExport         *bird.Export
	routeService       *RouteService
	log                *zap.SugaredLogger
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(cfg *Config, log *zap.SugaredLogger) (*RouteModule, error) {
	log = log.With(zap.String("module", "routepb.RouteService"))

	neighbourCache := discovery.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	neighbourDiscovery := neigh.NewNeighMonitor(neighbourCache, neigh.WithLog(log))

	rib := rib.NewRIB(neighbourCache, log)

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", cfg.MemoryPath, err)
	}

	numaIndices := shm.NumaIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("numa", numaIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("route", numaIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	server := grpc.NewServer()

	routeService := NewRouteService(agents, rib, log)
	routepb.RegisterRouteServiceServer(server, routeService)

	neighbourService := NewNeighbourService(neighbourCache, log)
	routepb.RegisterNeighbourServer(server, neighbourService)

	export := bird.NewExportReader(cfg.BirdExport, routeService, log)

	return &RouteModule{
		cfg:                cfg,
		server:             server,
		shm:                shm,
		agents:             agents,
		neighbourDiscovery: neighbourDiscovery,
		birdExport:         export,
		routeService:       routeService,
		log:                log,
	}, nil
}

// Close closes the module.
func (m *RouteModule) Close() error {
	for numaIdx, agent := range m.agents {
		if err := agent.Close(); err != nil {
			m.log.Warnw("failed to close shared memory agent", zap.Int("numa", numaIdx), zap.Error(err))
		}
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}

// Run runs the module until the specified context is canceled.
func (m *RouteModule) Run(ctx context.Context) error {
	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.neighbourDiscovery.Run(ctx)
	})
	wg.Go(func() error {
		return m.birdExport.Run(ctx)
	})
	wg.Go(func() error {
		return m.routeService.periodicRIBFlusher(ctx, m.cfg.RIBFlushPeriod)
	})

	listener, err := net.Listen("tcp", m.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to initialize gRPC listener: %w", err)
	}

	wg.Go(func() error {
		m.log.Infow("exposing gRPC API", zap.Stringer("addr", listener.Addr()))
		return m.server.Serve(listener)
	})

	serviceNames := []string{
		"routepb.RouteService",
		"routepb.Neighbour",
	}

	if err := gateway.RegisterModule(
		ctx,
		m.cfg.GatewayEndpoint,
		listener,
		serviceNames,
		m.log,
	); err != nil {
		return fmt.Errorf("failed to register services: %w", err)
	}

	<-ctx.Done()

	m.log.Infow("stopping gRPC API", zap.Stringer("addr", listener.Addr()))
	defer m.log.Infow("stopped gRPC API", zap.Stringer("addr", listener.Addr()))

	m.server.GracefulStop()

	return wg.Wait()
}
