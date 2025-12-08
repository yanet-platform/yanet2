package acl

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const agentName = "acl"
const serviceName = "aclpb.AclService"

// AclModule implements module for Acl control
type AclModule struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agents  []*ffi.Agent
	service *ACLService
	log     *zap.SugaredLogger
}

// NewAclModule creates a new Acl module instance
func NewAclModule(cfg *Config, log *zap.SugaredLogger) (*AclModule, error) {
	log = log.With(zap.String("module", serviceName))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	instanceIndices := shm.InstanceIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("instances", instanceIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach(agentName, instanceIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agents: %w", err)
	}

	service := NewACLService(agents)

	return &AclModule{
		cfg:     cfg,
		shm:     shm,
		agents:  agents,
		service: service,
		log:     log,
	}, nil
}

func (m *AclModule) Name() string {
	return "acl"
}

func (m *AclModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *AclModule) ServicesNames() []string {
	return []string{serviceName}
}

func (m *AclModule) RegisterService(server *grpc.Server) {
	aclpb.RegisterAclServiceServer(server, m.service)
}

func (m *AclModule) Close() error {
	for i, agent := range m.agents {
		if err := agent.Close(); err != nil {
			m.log.Warnw("failed to close shared memory agent", "instance", i, "error", err)
		}
	}
	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach shared memory", "error", err)
	}
	return nil
}
