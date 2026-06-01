// Package blackhole implements Blackhole module.
package blackhole

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const (
	agentName  = "blackhole"
	moduleName = "blackhole"
	configName = "blackhole0"
)

// Option configures the BlackholeModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the blackhole module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// BlackholeModule is a controlplane component for blackhole module.
type BlackholeModule struct {
	config *cblackhole.ModuleConfig
	shm    *ffi.SharedMemory
	agent  *ffi.Agent
	log    *zap.Logger
}

// NewBlackholeModule creates a new BlackholeModule.
func NewBlackholeModule(cfg *Config, options ...Option) (*BlackholeModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", moduleName))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	config, err := cblackhole.NewModuleConfig(agent, configName)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	log.Info("created module config", zap.String("name", configName))

	return &BlackholeModule{
		config: config,
		shm:    shm,
		agent:  agent,
		log:    log,
	}, nil
}

// Name returns the module name.
func (m *BlackholeModule) Name() string {
	return moduleName
}

// Endpoint returns the gRPC endpoint for the blackhole module.
func (m *BlackholeModule) Endpoint() string {
	return ""
}

// ServicesNames returns the gRPC service names exposed by the module.
func (m *BlackholeModule) ServicesNames() []string {
	return nil
}

// RegisterService registers the blackhole module's gRPC service.
func (m *BlackholeModule) RegisterService(server *grpc.Server) {
}

// Close releases shared memory resources held by the module.
func (m *BlackholeModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}
	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach shared memory", zap.Error(err))
	}

	return nil
}
