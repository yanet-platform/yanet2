package balancer

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/service"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const agentName = "balancer"

// BalancerModule is a control-plane component of a module that is responsible
// for balancing traffic.
type BalancerModule struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *service.BalancerService
	log     *zap.SugaredLogger
}

func NewBalancerModule(
	cfg *Config,
	log *zap.SugaredLogger,
) (*BalancerModule, error) {
	log = log.With(zap.String("module", "balancerpb.BalancerService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory: %w", err)
	}

	log.Debugw("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	svc := service.NewBalancerService(agent, log)

	return &BalancerModule{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: svc,
		log:     log,
	}, nil
}

func (m *BalancerModule) Name() string {
	return agentName
}

func (m *BalancerModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *BalancerModule) ServicesNames() []string {
	return []string{"balancerpb.BalancerService"}
}

func (m *BalancerModule) RegisterService(server *grpc.Server) {
	balancerpb.RegisterBalancerServiceServer(server, m.service)
}

func (m *BalancerModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
