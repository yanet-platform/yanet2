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
	agentName          = "acl"
	serviceName        = "modules.acl.controlplane.aclpb.v1.ACLService"
	fwstateServiceName = "modules.fwstate.controlplane.fwstatepb.v1.FWStateService"
)

// ACLModule is a control-plane component for ACL (Access Control List) module
// with integrated firewall state management.
type ACLModule struct {
	cfg            *Config
	shm            *ffi.SharedMemory
	agent          *ffi.Agent
	aclService     *ACLService
	metricsService *MetricsService
	fwstateService *fwstate.FWStateService
	log            *zap.Logger
}

// NewACLModule creates a new ACL module instance.
func NewACLModule(cfg *Config, log *zap.Logger) (*ACLModule, error) {
	log = log.With(zap.String("module", serviceName))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	aclService := NewACLService(
		NewBackend(agent, uint64(cfg.MemoryRequirements.Unwrap().Bytes())),
		WithLog(log),
		WithMetrics(grpcmetrics.NewFactory(
			grpcmetrics.WithLabeler(labeler),
		)),
	)

	metricsService := NewMetricsService(aclService)

	aclAdapter := NewACLAdapter(aclService)
	fwstateService := fwstate.NewFWStateService(agent, aclAdapter, log)

	return &ACLModule{
		cfg:            cfg,
		shm:            shm,
		agent:          agent,
		aclService:     aclService,
		metricsService: metricsService,
		fwstateService: fwstateService,
		log:            log,
	}, nil
}

func (m *ACLModule) Name() string {
	return "acl"
}

func (m *ACLModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *ACLModule) ServicesNames() []string {
	return []string{serviceName, aclpb.MetricsService_ServiceDesc.ServiceName, fwstateServiceName}
}

func (m *ACLModule) RegisterService(server *grpc.Server) {
	aclpb.RegisterACLServiceServer(server, m.aclService)
	aclpb.RegisterMetricsServiceServer(server, m.metricsService)
	fwstatepb.RegisterFWStateServiceServer(server, m.fwstateService)
}

// UnaryServerInterceptors returns the gRPC unary interceptors for this module.
func (m *ACLModule) UnaryServerInterceptors() []grpc.UnaryServerInterceptor {
	si := m.aclService.UnaryServerInterceptor()
	if si == nil {
		return nil
	}

	return []grpc.UnaryServerInterceptor{si}
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
