package route

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/neigh"
)

const (
	agentName = "route"

	// defaultStaticPriority is the default priority for statically
	// configured neighbours.
	defaultStaticPriority = 10
)

// RouteModule is a controlplane part of a module that is responsible for
// routing configuration.
type RouteModule struct {
	cfg              *Config
	shm              *cpffi.SharedMemory
	agent            *cpffi.Agent
	neighbourMonitor *neigh.NeighMonitor
	routeService     *RouteService
	neighbourService *NeighbourService
	log              *zap.Logger
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(cfg *Config, log *zap.Logger) (*RouteModule, error) {
	log = log.With(zap.String("module", "routepb.RouteService"))

	neighbourTable := neigh.NewNeighTable()
	if _, err := neighbourTable.CreateSource("static", defaultStaticPriority, true); err != nil {
		return nil, fmt.Errorf("failed to create static neighbour source: %w", err)
	}

	neighbourMonitor, err := newNeighbourMonitor(cfg, neighbourTable, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create neighbour monitor: %w", err)
	}

	shm, err := cpffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", cfg.MemoryPath, err)
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentReattach(agentName, cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	routeService := NewRouteService(NewBackend(agent, log), neighbourTable, cfg.RibTTL, log)
	neighbourService := NewNeighbourService(neighbourTable)

	return &RouteModule{
		cfg:              cfg,
		shm:              shm,
		agent:            agent,
		neighbourMonitor: neighbourMonitor,
		routeService:     routeService,
		neighbourService: neighbourService,
		log:              log,
	}, nil
}

// newNeighbourMonitor creates a new neighbour monitor if netlink discovery is
// enabled.
func newNeighbourMonitor(
	cfg *Config,
	neighTable *neigh.NeighTable,
	log *zap.Logger,
) (*neigh.NeighMonitor, error) {
	if cfg.NetlinkMonitor.Disabled {
		return nil, nil
	}

	source, err := neighTable.CreateSource(
		cfg.NetlinkMonitor.TableName,
		cfg.NetlinkMonitor.DefaultPriority,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create kernel neighbour source: %w", err)
	}

	neighbourMonitor := neigh.NewNeighMonitor(
		neighTable,
		source,
		neigh.WithLog(log),
		neigh.WithLinkMap(cfg.LinkMap),
	)

	return neighbourMonitor, nil
}

func (m *RouteModule) Name() string {
	return agentName
}

func (m *RouteModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
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
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}

// Run runs the module until the specified context is canceled.
// Implements the BackgroundBuiltInModule interface from
// controlplane/internal/gateway/runner.go
func (m *RouteModule) Run(ctx context.Context) error {
	wg, ctx := errgroup.WithContext(ctx)

	wg.Go(func() error {
		return m.runNeighbourMonitor(ctx)
	})
	wg.Go(func() error {
		<-ctx.Done()
		close(m.routeService.quitCh)
		return ctx.Err()
	})

	return wg.Wait()
}

func (m *RouteModule) runNeighbourMonitor(ctx context.Context) error {
	if m.neighbourMonitor == nil {
		return nil
	}

	return m.neighbourMonitor.Run(ctx)
}
