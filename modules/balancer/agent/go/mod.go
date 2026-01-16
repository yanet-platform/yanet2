package balancer

import (
	"fmt"

	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type BalancerModule struct {
	cfg     *Config
	service *BalancerService
}

func NewBalancerModule(
	cfg *Config,
	log *zap.SugaredLogger,
) (*BalancerModule, error) {
	log = log.With(zap.String("module", "balancerpb.BalancerService"))

	shm, err := yanet.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	svc, err := NewBalancerService(shm, cfg.MemoryRequirements, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create balancer service: %w", err)
	}

	return &BalancerModule{
		cfg:     cfg,
		service: svc,
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

func (m *BalancerModule) Close() error {
	return nil
}
