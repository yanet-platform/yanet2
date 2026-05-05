package balancer2

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
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
	service *Service
	log     *zap.Logger
}

func NewModule(cfg *Config, options ...Option) (*Module, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "balancerpb.Balancer"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	service, err := NewService(
		shm,
		cfg.InstanceID,
		cfg.MemoryRequirements.Unwrap(),
		WithServiceLog(log),
	)
	if err != nil {
		err = fmt.Errorf("failed to create balancer service: %w", err)
		if detachErr := shm.Detach(); detachErr != nil {
			err = errors.Join(err, fmt.Errorf("detach shared memory: %w", detachErr))
		}
		return nil, err
	}

	return &Module{
		cfg:     cfg,
		shm:     shm,
		service: service,
		log:     log,
	}, nil
}

func (m *Module) Name() string {
	return "balancer"
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

// Close releases the service and detaches from shared memory.
func (m *Module) Close() error {
	var errs []error
	if err := m.service.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close balancer service: %w", err))
	}
	if err := m.shm.Detach(); err != nil {
		errs = append(errs, fmt.Errorf("detach shared memory: %w", err))
	}
	return errors.Join(errs...)
}
