package route_mpls

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
)

const (
	agentName = "route-mpls"
)

// Option configures the RouteMPLSModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the route-mpls module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// RouteMPLSModule is the controlplane part of the route-mpls module that owns
// shared memory and exposes the routemplspb.RouteMPLSService gRPC surface.
type RouteMPLSModule struct {
	cfg     *Config
	shm     *cpffi.SharedMemory
	agent   *cpffi.Agent
	service *RouteMPLSService
	log     *zap.Logger
}

// NewRouteMPLSModule creates a new RouteMPLSModule.
func NewRouteMPLSModule(cfg *Config, options ...Option) (*RouteMPLSModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "modules.route_mpls.controlplane.routemplspb.v1.RouteMPLSService"))

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

	service := NewRouteMPLSService(NewBackend(agent), WithRouteMPLSServiceLog(log))

	return &RouteMPLSModule{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: service,
		log:     log,
	}, nil
}

// Name returns the module name.
func (m *RouteMPLSModule) Name() string {
	return agentName
}

// Endpoint returns the gRPC endpoint for the route-mpls module.
func (m *RouteMPLSModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *RouteMPLSModule) ServicesNames() []string {
	return []string{
		"modules.route_mpls.controlplane.routemplspb.v1.RouteMPLSService",
	}
}

// RegisterService registers the route-mpls module's gRPC service.
func (m *RouteMPLSModule) RegisterService(server *grpc.Server) {
	routemplspb.RegisterRouteMPLSServiceServer(server, m.service)
}

// Close closes the module.
func (m *RouteMPLSModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}
	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach from shared memory mapping", zap.Error(err))
	}
	return nil
}
