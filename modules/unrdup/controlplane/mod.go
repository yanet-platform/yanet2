package unrdup

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

// Option configures the UnrdupModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the unrdup module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// UnrdupModule is the control-plane component of the unrdup module.
type UnrdupModule struct {
	cfg           *Config
	shm           *ffi.SharedMemory
	agent         *ffi.Agent
	unrdupService *UnrdupService
}

func NewUnrdupModule(cfg *Config, options ...Option) (*UnrdupModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "unrdup"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug(
		"mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("unrdup", cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			shm.Detach(),
		)
	}

	return &UnrdupModule{
		cfg:           cfg,
		shm:           shm,
		agent:         agent,
		unrdupService: NewUnrdupService(newBackend(agent)),
	}, nil
}

func (m *UnrdupModule) Name() string {
	return "unrdup"
}

func (m *UnrdupModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *UnrdupModule) ServicesNames() []string {
	return []string{"modules.unrdup.controlplane.unrduppb.v1.UnrdupService"}
}

func (m *UnrdupModule) RegisterService(server *grpc.Server) {
	unrduppb.RegisterUnrdupServiceServer(server, m.unrdupService)
}

// Close closes the module.
func (m *UnrdupModule) Close() error {
	return errors.Join(m.agent.Close(), m.shm.Detach())
}
