package balancer

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

// BalancerModule is a control-plane component responsible for L3-balancer
type BalancerModule struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agents  []*ffi.Agent
	service *BalancerService
	log     *zap.SugaredLogger
}

// NewBalancerModule creates a new Balancer module instance
func NewBalancerModule(cfg *Config, log *zap.SugaredLogger) (*BalancerModule, error) {
	log = log.With(zap.String("module", "balancerpb.BalancerService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	instanceIndices := shm.InstanceIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("instances", instanceIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("balancer", instanceIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	balancerService := NewBalancerService(agents, log)

	return &BalancerModule{
		cfg:     cfg,
		shm:     shm,
		agents:  agents,
		service: balancerService,
		log:     log,
	}, nil
}

func (m *BalancerModule) Name() string {
	return "balancer"
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

// Close closes the module and releases all resources
func (m *BalancerModule) Close() error {
	for instance, agent := range m.agents {
		if err := agent.Close(); err != nil {
			m.log.Warnw("failed to close shared memory agent", zap.Int("instance", instance), zap.Error(err))
		}
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
