package proxy

import (
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/modules/proxy/controlplane/proxypb"
)

const agentName = "proxy"

type ProxyModule struct {
	cfg          *Config
	shm          *ffi.SharedMemory
	agents       []*ffi.Agent
	proxyService *ProxyService
	log          *zap.SugaredLogger
}

func NewProxyModule(cfg *Config, log *zap.SugaredLogger) (*ProxyModule, error) {
	log = log.With(zap.String("module", "proxypb.ProxyService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	instances := shm.InstanceIndices()
	log.Debugw("mapped shared memory",
		zap.Uint32s("instances", instances),
		zap.Stringer("size", cfg.MemoryRequirements))

	agents, err := shm.AgentsAttach(agentName, instances, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	proxyService := NewProxyService(agents, log)

	return &ProxyModule{
		cfg:          cfg,
		shm:          shm,
		agents:       agents,
		proxyService: proxyService,
		log:          log,
	}, nil
}

func (m *ProxyModule) Name() string {
	return agentName
}

func (m *ProxyModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *ProxyModule) ServicesNames() []string {
	return []string{"proxypb.ProxyService"}
}

func (m *ProxyModule) RegisterService(server *grpc.Server) {
	proxypb.RegisterProxyServiceServer(server, m.proxyService)
}

// Close closes the module.
func (m *ProxyModule) Close() error {
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
