package forward

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

const agentName = "forward"

// ForwardModule is a control-plane component of a module that is responsible for
// forwarding traffic between devices.
type ForwardModule struct {
	cfg            *Config
	shm            *ffi.SharedMemory
	agent          *ffi.Agent
	forwardService *ForwardService
	log            *zap.SugaredLogger
}

func NewForwardModule(cfg *Config, log *zap.SugaredLogger) (*ForwardModule, error) {
	log = log.With(zap.String("module", "forwardpb.ForwardService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	log.Debugw("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, cfg.MemoryRequirements)
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	forwardService := NewForwardService(agent)

	return &ForwardModule{
		cfg:            cfg,
		shm:            shm,
		agent:          agent,
		forwardService: forwardService,
		log:            log,
	}, nil
}

func (m *ForwardModule) Name() string {
	return agentName
}

func (m *ForwardModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *ForwardModule) ServicesNames() []string {
	return []string{"forwardpb.ForwardService"}
}

func (m *ForwardModule) RegisterService(server *grpc.Server) {
	forwardpb.RegisterForwardServiceServer(server, m.forwardService)
}

// Close closes the module.
func (m *ForwardModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
