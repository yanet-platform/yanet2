package acl

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	"github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb"
)

const agentName = "acl"
const serviceName = "aclpb.ACLService"
const fwstateServiceName = "fwstatepb.FWStateService"

// ACLModule is a control-plane component for ACL (Access Control List) module
// with integrated firewall state management
type ACLModule struct {
	cfg            *Config
	shm            *ffi.SharedMemory
	agent          *ffi.Agent
	aclService     *ACLService
	fwstateService *fwstate.FWStateService
	log            *zap.SugaredLogger
}

// NewACLModule creates a new ACL module instance
func NewACLModule(cfg *Config, log *zap.SugaredLogger) (*ACLModule, error) {
	log = log.With(zap.String("module", serviceName))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	log.Debugw("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, cfg.MemoryRequirements)
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	aclService := NewACLService(agent, log)
	aclAdapter := NewACLAdapter(aclService)
	fwstateService := fwstate.NewFWStateService(agent, aclAdapter, log)

	return &ACLModule{
		cfg:            cfg,
		shm:            shm,
		agent:          agent,
		aclService:     aclService,
		fwstateService: fwstateService,
		log:            log,
	}, nil
}

func (m *ACLModule) Name() string {
	return "acl"
}

func (m *ACLModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *ACLModule) ServicesNames() []string {
	return []string{serviceName, fwstateServiceName}
}

func (m *ACLModule) RegisterService(server *grpc.Server) {
	aclpb.RegisterACLServiceServer(server, m.aclService)
	fwstatepb.RegisterFWStateServiceServer(server, m.fwstateService)
}

// ACLAdapter returns an adapter for fwstate module integration
func (m *ACLModule) ACLAdapter() *ACLAdapter {
	return NewACLAdapter(m.aclService)
}

func (m *ACLModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", "error", err)
	}
	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach shared memory", "error", err)
	}

	return nil
}
