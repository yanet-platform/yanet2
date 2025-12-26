package nat64

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb"
)

// NAT64Module is a control-plane component responsible for NAT64 translation
type NAT64Module struct {
	cfg          *Config
	shm          *ffi.SharedMemory
	agent        *ffi.Agent
	nat64Service *NAT64Service
	log          *zap.SugaredLogger
}

// NewNAT64Module creates a new NAT64 module instance
func NewNAT64Module(cfg *Config, log *zap.SugaredLogger) (*NAT64Module, error) {
	log = log.With(zap.String("module", "nat64pb.NAT64Service"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	log.Debugw("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("nat64", cfg.InstanceID, cfg.MemoryRequirements)
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	nat64Service := NewNAT64Service(agent, log)

	return &NAT64Module{
		cfg:          cfg,
		shm:          shm,
		agent:        agent,
		nat64Service: nat64Service,
		log:          log,
	}, nil
}

func (m *NAT64Module) Name() string {
	return "nat64"
}

func (m *NAT64Module) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *NAT64Module) ServicesNames() []string {
	return []string{"nat64pb.NAT64Service"}
}

func (m *NAT64Module) RegisterService(server *grpc.Server) {
	nat64pb.RegisterNAT64ServiceServer(server, m.nat64Service)
}

// Close closes the module and releases all resources
func (m *NAT64Module) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
