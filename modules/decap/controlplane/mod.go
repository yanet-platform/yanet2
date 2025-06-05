package decap

import (
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
	agents       []*ffi.Agent
	decapService *DecapService
	log          *zap.SugaredLogger
}

func NewDecapModule(cfg *Config, log *zap.SugaredLogger) (*DecapModule, error) {
	log = log.With(zap.String("module", "decappb.DecapService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	instanceIndices := shm.InstanceIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("instances", instanceIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("decap", instanceIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	decapService := NewDecapService(agents, log)

	return &DecapModule{
		cfg:          cfg,
		shm:          shm,
		agents:       agents,
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
