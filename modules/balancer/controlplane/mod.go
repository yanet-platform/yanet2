package balancer

import (
	"fmt"

	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type BalancerModule struct {
	cfg     *Config
	shm     *yanet.SharedMemory
	service *BalancerService
}

func NewBalancerModule(
	cfg *Config,
	log *zap.SugaredLogger,
) (*BalancerModule, error) {
	log = log.With(zap.String("module", "balancerpb.Balancer"))

	shm, err := yanet.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	svc, err := NewBalancerService(shm, cfg.InstanceID, cfg.MemoryRequirements.Unwrap(), log)
	if err != nil {
		shm.Detach()
		return nil, fmt.Errorf("failed to create balancer service: %w", err)
	}

	return &BalancerModule{
		cfg:     cfg,
		shm:     shm,
		service: svc,
	}, nil
}

func (m *BalancerModule) Name() string {
	return "balancer"
}

func (m *BalancerModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *BalancerModule) ServicesNames() []string {
	return []string{"balancerpb.Balancer"}
}

func (m *BalancerModule) RegisterService(server *grpc.Server) {
	balancerpb.RegisterBalancerServer(server, m.service)
}

func (m *BalancerModule) Close() error {
	return m.shm.Detach()
}
