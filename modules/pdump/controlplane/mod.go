package pdump

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
)

// Option configures the PdumpModule constructor.
type Option func(*moduleOptions)

type moduleOptions struct {
	Log *zap.Logger
}

func newModuleOptions() *moduleOptions {
	return &moduleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the pdump module.
func WithLog(log *zap.Logger) Option {
	return func(o *moduleOptions) {
		o.Log = log
	}
}

// PdumpModule is a control-plane component of a packet dump module.
type PdumpModule struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *PdumpService
	log     *zap.Logger
}

func NewPdumpModule(cfg *Config, options ...Option) (*PdumpModule, error) {
	opts := newModuleOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "modules.pdump.controlplane.pdumppb.v1.PdumpService"))

	// setup CGO export logger
	logger = log.WithOptions(
		zap.WithCaller(false),
		zap.AddStacktrace(zapcore.FatalLevel),
	)
	debugEBPF = cfg.DebugEBPF

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(moduleType, cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	service := NewPdumpService(agent, WithPdumpServiceLog(log))

	return &PdumpModule{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: service,
		log:     log,
	}, nil
}

func (m *PdumpModule) Name() string {
	return moduleType
}

func (m *PdumpModule) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *PdumpModule) ServicesNames() []string {
	return []string{"modules.pdump.controlplane.pdumppb.v1.PdumpService"}
}

func (m *PdumpModule) RegisterService(server *grpc.Server) {
	pdumppb.RegisterPdumpServiceServer(server, m.service)
}

// Run runs the module until the specified context is canceled.
// Implements the gateway.BackgroundService interface.
func (m *PdumpModule) Run(ctx context.Context) error {
	<-ctx.Done()
	m.log.Info("closing pdump service")
	m.service.Shutdown()
	return nil
}

// Close closes the module.
func (m *PdumpModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
