package proxy

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/modules/proxy/controlplane/proxypb"
)

const agentName = "proxy"

type ProxyModule struct {
	cfg          *Config
	shm          *ffi.SharedMemory
	agent        *ffi.Agent
	proxyService *ProxyService
	log          *zap.SugaredLogger
}

func NewProxyModule(cfg *Config, log *zap.SugaredLogger) (*ProxyModule, error) {
	log = log.With(zap.String("module", "proxypb.ProxyService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	log.Debugw("mapped shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements))

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, cfg.MemoryRequirements)
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	proxyService := NewProxyService(agent, log)

	return &ProxyModule{
		cfg:          cfg,
		shm:          shm,
		agent:        agent,
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
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
