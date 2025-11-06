package acl

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// ACLModule реализует модуль управления ACL
type ACLModule struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agents  []*ffi.Agent
	service *ACLService
	log     *zap.SugaredLogger
}

// NewACLModule creates a new ACL module instance
func NewACLModule(cfg *Config, log *zap.SugaredLogger) (*ACLModule, error) {
	log = log.With(zap.String("module", "acl"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	instanceIndices := shm.InstanceIndices()
	log.Debugw("attached to shared memory",
		"instances", instanceIndices,
		"size", cfg.MemoryRequirements,
	)

	agents, err := shm.AgentsAttach("acl", instanceIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agents: %w", err)
	}

	service := NewACLService(agents, log)

	return &ACLModule{
		cfg:     cfg,
		shm:     shm,
		agents:  agents,
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
	return []string{"aclpb.ACLService"}
}

func (m *ACLModule) RegisterService(server *grpc.Server) {
	aclpb.RegisterACLServiceServer(server, m.service)
}

func (m *ACLModule) Close() error {
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
