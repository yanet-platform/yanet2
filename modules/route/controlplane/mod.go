package route

import (
	"context"
	"fmt"
	"net/netip"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/neigh"
)

// RouteModule is a controlplane part of a module that is responsible for
// routing configuration.
type RouteModule struct {
	cfg                *Config
	shm                *ffi.SharedMemory
	agents             []*ffi.Agent
	neighbourDiscovery *neigh.NeighMonitor
	routeService       *RouteService
	neighbourService   *NeighbourService
	log                *zap.SugaredLogger
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(cfg *Config, log *zap.SugaredLogger) (*RouteModule, error) {
	log = log.With(zap.String("module", "routepb.RouteService"))

	neighbourCache := discovery.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	neighbourDiscovery := neigh.NewNeighMonitor(neighbourCache, neigh.WithLog(log))

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

	routeService := NewRouteService(agents, neighbourCache, log)
	neighbourService := NewNeighbourService(neighbourCache, log)

	return &RouteModule{
		cfg:                cfg,
		shm:                shm,
		agents:             agents,
		neighbourDiscovery: neighbourDiscovery,
		routeService:       routeService,
		neighbourService:   neighbourService,
		log:                log,
	}, nil
}

func (m *RouteModule) Name() string {
	return "route"
}

func (m *RouteModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *RouteModule) ServicesNames() []string {
	return []string{
		"routepb.RouteService",
		"routepb.Neighbour",
	}
}

func (m *RouteModule) RegisterService(server *grpc.Server) {
	routepb.RegisterRouteServiceServer(server, m.routeService)
	routepb.RegisterNeighbourServer(server, m.neighbourService)
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

	return wg.Wait()
}
