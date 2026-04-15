package balancer

import (
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type Module struct {
	cfg     *Config
	shm     *yanet.SharedMemory
	service *Service
}

func NewModule(
	cfg *Config,
	log *zap.SugaredLogger,
) (*Module, error) {
	log = log.With(zap.String("module", "balancerpb.Balancer"))

	shm, err := yanet.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, NewError("failed to attach shared memory: %w", err)
	}

	svc, err := NewService(shm, cfg.InstanceID, cfg.MemoryRequirements.Unwrap(), log)
	if err != nil {
		_ = shm.Detach()
		return nil, NewError("failed to create balancer service: %w", err)
	}

	return &Module{
		cfg:     cfg,
		shm:     shm,
		service: svc,
	}, nil
}

func (m *Module) Name() string {
	return "balancer"
}

func (m *Module) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *Module) ServicesNames() []string {
	return []string{"balancerpb.Balancer"}
}

func (m *Module) RegisterService(server *grpc.Server) {
	balancerpb.RegisterBalancerServer(server, m.service)
}

func (m *Module) Close() error {
	m.service.mu.Lock()
	defer m.service.mu.Unlock()

	m.service.agent.Close()

	return m.shm.Detach()
}
