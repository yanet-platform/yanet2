// Package l3b implements L3b module.
package l3b

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
)

// Option configures the L3BModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the l3b module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// L3BModule is the control-plane component of the l3b module.
type L3BModule struct {
	cfg        *Config
	shm        *ffi.SharedMemory
	agent      *ffi.Agent
	l3bService *L3BService
}

func NewL3BModule(cfg *Config, options ...Option) (*L3BModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "l3b"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug(
		"mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("l3b", cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			shm.Detach(),
		)
	}

	return &L3BModule{
		cfg:        cfg,
		shm:        shm,
		agent:      agent,
		l3bService: NewL3BService(NewBackend(agent)),
	}, nil
}

func (m *L3BModule) Name() string {
	return "l3b"
}

func (m *L3BModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *L3BModule) ServicesNames() []string {
	return []string{"modules.l3b.controlplane.l3bpb.v1.L3bService"}
}

func (m *L3BModule) RegisterService(server *grpc.Server) {
	l3bpb.RegisterL3BServiceServer(server, m.l3bService)
}

// Close closes the module.
func (m *L3BModule) Close() error {
	return errors.Join(m.agent.Close(), m.shm.Detach())
}
