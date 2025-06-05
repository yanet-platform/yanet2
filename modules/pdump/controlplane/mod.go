package pdump

import (
	"context"

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
	agents  []*ffi.Agent
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

	instanceIndices := shm.InstanceIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("instances", instanceIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("pdump", instanceIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	service := NewPdumpService(agents, log)

	return &PdumpModule{
		cfg:     cfg,
		shm:     shm,
		agents:  agents,
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
	for inst, agent := range m.agents {
		if err := agent.Close(); err != nil {
			m.log.Warnw("failed to close shared memory agent", zap.Int("instance", inst), zap.Error(err))
		}
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
