package decap

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb"
)

// DecapModule is a control-plane component of a module that is responsible for
// decapsulating various kinds of tunnels.
type DecapModule struct {
	cfg          *Config
	shm          *ffi.SharedMemory
	agent        *ffi.Agent
	decapService *DecapService
	log          *zap.SugaredLogger
}

func NewDecapModule(cfg *Config, log *zap.SugaredLogger) (*DecapModule, error) {
	log = log.With(zap.String("module", "decappb.DecapService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	log.Debugw("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("decap", cfg.InstanceID, cfg.MemoryRequirements)
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	decapService := NewDecapService(agent, log)

	return &DecapModule{
		cfg:          cfg,
		shm:          shm,
		agent:        agent,
		decapService: decapService,
		log:          log,
	}, nil
}

func (m *DecapModule) Name() string {
	return "decap"
}

func (m *DecapModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *DecapModule) ServicesNames() []string {
	return []string{"decappb.DecapService"}
}

func (m *DecapModule) RegisterService(server *grpc.Server) {
	decappb.RegisterDecapServiceServer(server, m.decapService)
}

// Close closes the module.
func (m *DecapModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
