package decap

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// Option configures the DecapModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the decap module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// DecapModule is a control-plane component of a module that is responsible for
// decapsulating various kinds of tunnels.
type DecapModule struct {
	cfg          *Config
	shm          *ffi.SharedMemory
	agent        *ffi.Agent
	decapService *DecapService
	log          *zap.Logger
}

func NewDecapModule(cfg *Config, options ...Option) (*DecapModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "modules.decap.controlplane.decappb.v1.DecapService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("decap", cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	decapService := NewDecapService(NewBackend(agent))

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
	return m.cfg.Endpoint.Unwrap()
}

func (m *DecapModule) ServicesNames() []string {
	return []string{"modules.decap.controlplane.decappb.v1.DecapService"}
}

func (m *DecapModule) RegisterService(server *grpc.Server) {
	decappb.RegisterDecapServiceServer(server, m.decapService)
}

// Close closes the module.
func (m *DecapModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
