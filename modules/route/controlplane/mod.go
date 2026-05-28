package route

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

const (
	agentName = "route"
)

// Option configures the RouteModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the route module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// RouteModule is the slim route-module shim that owns shared memory and
// exposes the routepb.RouteService gRPC surface.
//
// The module no longer owns a RIB or a neighbour table; the
// yanet-route-operator agent rebuilds the FIB and pushes it via
// UpdateFIB.
type RouteModule struct {
	cfg     *Config
	shm     *cpffi.SharedMemory
	agent   *cpffi.Agent
	service *RouteService
	log     *zap.Logger
}

// NewRouteModule creates a new RouteModule.
func NewRouteModule(cfg *Config, options ...Option) (*RouteModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "routepb.RouteService"))

	shm, err := cpffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", cfg.MemoryPath, err)
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	service := NewRouteService(NewBackend(agent), WithRouteServiceLog(log))

	return &RouteModule{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: service,
		log:     log,
	}, nil
}

// Name returns the module name.
func (m *RouteModule) Name() string {
	return agentName
}

// Endpoint returns the gRPC endpoint for the route module shim.
func (m *RouteModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *RouteModule) ServicesNames() []string {
	return []string{
		"routepb.RouteService",
	}
}

// RegisterService registers the route module's gRPC service.
func (m *RouteModule) RegisterService(server *grpc.Server) {
	routepb.RegisterRouteServiceServer(server, m.service)
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
