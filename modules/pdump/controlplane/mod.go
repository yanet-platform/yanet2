package pdump

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb"
)

// PdumpModule is a control-plane component of a packet dump module.
type PdumpModule struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *PdumpService
	log     *zap.SugaredLogger
}

func NewPdumpModule(cfg *Config, log *zap.SugaredLogger) (*PdumpModule, error) {
	log = log.With(zap.String("module", "pdumppb.PdumpService"))

	// setup CGO export logger
	logger = log.WithOptions(
		zap.WithCaller(false),
		zap.AddStacktrace(zapcore.FatalLevel),
	)
	debugEBPF = cfg.DebugEBPF

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	log.Debugw("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("pdump", cfg.InstanceID, cfg.MemoryRequirements)
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	service := NewPdumpService(agent, log)

	return &PdumpModule{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: service,
		log:     log,
	}, nil
}

func (m *PdumpModule) Name() string {
	return "pdump"
}

func (m *PdumpModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *PdumpModule) ServicesNames() []string {
	return []string{"pdumppb.PdumpService"}
}

func (m *PdumpModule) RegisterService(server *grpc.Server) {
	pdumppb.RegisterPdumpServiceServer(server, m.service)
}

// Run runs the module until the specified context is canceled.
// Implements the BackgroundBuiltInModule interface from
// controlplane/internal/gateway/runner.go
func (m *PdumpModule) Run(ctx context.Context) error {
	<-ctx.Done()
	m.log.Info("closing pdump service")
	close(m.service.quitCh)
	return nil
}

// Close closes the module.
func (m *PdumpModule) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warnw("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
