package fwstate

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

const agentName = moduleType

// FWStateModule is a control-plane component of the firewall-state sync
// module.
//
// The module shares state with ACL solely through named fwstate-map
// objects: the map service publishes them into the instance-wide config
// generation, where module configs of any agent resolve them by name.
type FWStateModule struct {
	cfg                   *Config
	shm                   *ffi.SharedMemory
	agent                 *ffi.Agent
	fwstateService        *FWStateService
	fwstateMetricsService *MetricsService
	mapService            *fwstatemap.FWStateMapService
}

// NewFWStateModule creates a new fwstate module instance.
func NewFWStateModule(cfg *Config, options ...Option) (*FWStateModule, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", moduleType))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			shm.Detach(),
		)
	}

	// The map service owns the fwstate-map objects both this module's
	// configs and ACL configs link by name; a published object resolves
	// through the shared config generation regardless of the linking
	// module's agent.
	mapService := fwstatemap.NewFWStateMapService(
		agent,
		fwstatemap.WithLog(log),
		fwstatemap.WithMetrics(fwstatemap.NewMetricsFactory()),
	)

	fwstateService := NewFWStateService(
		agent,
		WithLog(log),
		WithMetrics(NewMetricsFactory()),
	)
	// The metrics endpoint aggregates the fwstate and map services, so
	// the gRPC metrics the map service's interceptor records are
	// reachable through the module's metrics RPC.
	fwstateMetricsService := NewMetricsService(fwstateService, mapService)

	return &FWStateModule{
		cfg:                   cfg,
		shm:                   shm,
		agent:                 agent,
		fwstateService:        fwstateService,
		fwstateMetricsService: fwstateMetricsService,
		mapService:            mapService,
	}, nil
}

func (m *FWStateModule) Name() string {
	return moduleType
}

func (m *FWStateModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *FWStateModule) ServicesNames() []string {
	return []string{
		FWStateServiceName,
		FWStateMetricsServiceName,
		fwstatemap.ServiceName,
	}
}

func (m *FWStateModule) RegisterService(server *grpc.Server) {
	fwstatepb.RegisterFWStateServiceServer(server, m.fwstateService)
	fwstatepb.RegisterMetricsServiceServer(server, m.fwstateMetricsService)
	m.mapService.Register(server)
}

// UnaryServerInterceptors returns the gRPC unary interceptors for this module.
func (m *FWStateModule) UnaryServerInterceptors() []grpc.UnaryServerInterceptor {
	var interceptors []grpc.UnaryServerInterceptor
	if si := m.fwstateService.UnaryServerInterceptor(); si != nil {
		interceptors = append(interceptors, si)
	}
	if si := m.mapService.UnaryServerInterceptor(); si != nil {
		interceptors = append(interceptors, si)
	}
	return interceptors
}

// Close closes the module.
func (m *FWStateModule) Close() error {
	return errors.Join(m.agent.Close(), m.shm.Detach())
}
