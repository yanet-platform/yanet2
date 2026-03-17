package route_mpls

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	route_mplspb "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/route-mplspb"
)

// RouteMPLSModule is a controlplane part of a module that is responsible for
// routing configuration.
type RouteMPLSModule struct {
	cfg              *Config
	shm              *ffi.SharedMemory
	agent            *ffi.Agent
	routeMPLSService *RouteMPLSService
	log              *zap.SugaredLogger
}

// NewRouteMPLSModule creates a new RouteMPLSModule.
func NewRouteMPLSModule(cfg *Config, log *zap.SugaredLogger) (*RouteMPLSModule, error) {
	log = log.With(zap.String("module", "routemplspb.RouteService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", cfg.MemoryPath, err)
	}

	log.Debugw("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("route-mpls", cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	routeMPLSService := NewRouteMPLSService(agent, log)

	return &RouteMPLSModule{
		cfg:              cfg,
		shm:              shm,
		agent:            agent,
		routeMPLSService: routeMPLSService,
		log:              log,
	}, nil
}

func (m *RouteMPLSModule) Name() string {
	return "route-mpls"
}

func (m *RouteMPLSModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *RouteMPLSModule) ServicesNames() []string {
	return []string{
		"routemplspb.RouteMPLSService",
	}
}

func (m *RouteMPLSModule) RegisterService(server *grpc.Server) {
	route_mplspb.RegisterRouteMPLSServiceServer(server, m.routeMPLSService)
}

// Close closes the module.
func (m *RouteMPLSModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
