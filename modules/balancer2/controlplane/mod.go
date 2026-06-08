package balancer2

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	balancerpb "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
)

// Option configures the Module constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the balancer module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

type Module struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *Service
	log     *zap.Logger
}

func NewBalancerModule(cfg *Config, options ...Option) (*Module, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if err := cfg.MemoryPath.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: memory path: %w", err)
	}
	if err := cfg.MemoryRequirements.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: memory requirements: %w", err)
	}

	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "balancerpb.Balancer"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	agent, err := shm.AgentAttach("balancer2", cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		err = fmt.Errorf("failed to reattach balancer agent: %w", err)
		if detachErr := shm.Detach(); detachErr != nil {
			err = errors.Join(err, fmt.Errorf("detach shared memory: %w", detachErr))
		}
		return nil, err
	}

	service := NewService(agent, WithServiceLog(log))

	return &Module{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: service,
		log:     log,
	}, nil
}

func (m *Module) Name() string {
	return "balancer2"
}

func (m *Module) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *Module) ServicesNames() []string {
	return []string{"balancerpb.Balancer"}
}

func (m *Module) RegisterService(server *grpc.Server) {
	balancerpb.RegisterBalancerServer(server, m.service)
}

// Close releases the service, the shared memory agent, and detaches from
// shared memory, in reverse order of construction.
func (m *Module) Close() error {
	var errs []error
	if err := m.service.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close balancer service: %w", err))
	}
	if err := m.agent.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close balancer agent: %w", err))
	}
	if err := m.shm.Detach(); err != nil {
		errs = append(errs, fmt.Errorf("detach shared memory: %w", err))
	}
	return errors.Join(errs...)
}
