package acl

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
)

const agentName = "acl"
const serviceName = "aclpb.ACLService"

// ACLModule is a control-plane component for ACL (Access Control List) module
// with integrated firewall state management
type ACLModule struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *ACLService
	log     *zap.SugaredLogger
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

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	service := NewACLService(agent, log)

	return &ACLModule{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: service,
		log:     log,
	}, nil
}

func (m *ACLModule) Name() string {
	return "acl"
}

func (m *ACLModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *ACLModule) ServicesNames() []string {
	return []string{serviceName}
}

func (m *ACLModule) RegisterService(server *grpc.Server) {
	aclpb.RegisterACLServiceServer(server, m.service)
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
