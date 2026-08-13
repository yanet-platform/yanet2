package acl

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	fwstatepb "github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

const (
	moduleType  = "acl"
	agentName   = moduleType
	serviceName = "modules.acl.controlplane.aclpb.v1.ACLService"
)

// ModuleOption configures the ACLModule constructor.
type ModuleOption func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithModuleLog sets the logger for the ACL module.
func WithModuleLog(log *zap.Logger) ModuleOption {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// ACLModule is a control-plane component for ACL (Access Control List) module
// with integrated firewall state management.
type ACLModule struct {
	cfg                   *Config
	shm                   *ffi.SharedMemory
	agent                 *ffi.Agent
	aclService            *ACLService
	metricsService        *MetricsService
	fwstateService        *fwstate.FWStateService
	fwstateMetricsService *fwstate.MetricsService
	log                   *zap.Logger
}

// NewACLModule creates a new ACL module instance.
func NewACLModule(cfg *Config, options ...ModuleOption) (*ACLModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", serviceName))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	aclService := NewACLService(
		NewBackend(agent),
		WithLog(log),
		WithMetrics(grpcmetrics.NewFactory(
			grpcmetrics.WithLabeler(labeler),
		)),
	)

	metricsService := NewMetricsService(aclService)

	aclAdapter := NewACLAdapter(aclService)
	fwstateService := fwstate.NewFWStateService(
		agent,
		aclAdapter,
		fwstate.WithLog(log),
		fwstate.WithMetrics(fwstate.NewMetricsFactory()),
	)
	fwstateMetricsService := fwstate.NewMetricsService(fwstateService)

	return &ACLModule{
		cfg:                   cfg,
		shm:                   shm,
		agent:                 agent,
		aclService:            aclService,
		metricsService:        metricsService,
		fwstateService:        fwstateService,
		fwstateMetricsService: fwstateMetricsService,
		log:                   log,
	}, nil
}

func (m *ACLModule) Name() string {
	return moduleType
}

func (m *ACLModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *ACLModule) ServicesNames() []string {
	return []string{
		serviceName,
		aclpb.MetricsService_ServiceDesc.ServiceName,
		fwstate.FWStateServiceName,
		fwstate.FWStateMetricsServiceName,
	}
}

func (m *ACLModule) RegisterService(server *grpc.Server) {
	aclpb.RegisterACLServiceServer(server, m.aclService)
	aclpb.RegisterMetricsServiceServer(server, m.metricsService)
	fwstatepb.RegisterFWStateServiceServer(server, m.fwstateService)
	fwstatepb.RegisterMetricsServiceServer(server, m.fwstateMetricsService)
}

// UnaryServerInterceptors returns the gRPC unary interceptors for this module.
func (m *ACLModule) UnaryServerInterceptors() []grpc.UnaryServerInterceptor {
	var interceptors []grpc.UnaryServerInterceptor
	if si := m.aclService.UnaryServerInterceptor(); si != nil {
		interceptors = append(interceptors, si)
	}
	if si := m.fwstateService.UnaryServerInterceptor(); si != nil {
		interceptors = append(interceptors, si)
	}
	return interceptors
}

// ACLAdapter returns an adapter for fwstate module integration.
func (m *ACLModule) ACLAdapter() *ACLAdapter {
	return NewACLAdapter(m.aclService)
}

func (m *ACLModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}
	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach shared memory", zap.Error(err))
	}

	return nil
}
