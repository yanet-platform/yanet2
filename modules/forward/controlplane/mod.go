package forward

import (
	"fmt"
	"math"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// ForwardModule is a control-plane component of a module that is responsible for
// forwarding traffic between devices.
type ForwardModule struct {
	cfg            *Config
	shm            *ffi.SharedMemory
	agents         []*ffi.Agent
	forwardService *ForwardService
	log            *zap.SugaredLogger
}

func NewForwardModule(cfg *Config, log *zap.SugaredLogger) (*ForwardModule, error) {
	log = log.With(zap.String("module", "forwardpb.ForwardService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	numaIndices := shm.NumaIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("numa", numaIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("forward", numaIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	// STATEMENT: All agents have the same topology.
	deviceCount := topologyDeviceCount(agents[0])
	if deviceCount >= math.MaxUint16 {
		return nil, fmt.Errorf("too many devices: %d (max %d)", deviceCount, math.MaxUint16)
	}

	forwardService := NewForwardService(agents, log, uint16(deviceCount))

	return &ForwardModule{
		cfg:            cfg,
		shm:            shm,
		agents:         agents,
		forwardService: forwardService,
		log:            log,
	}, nil
}

func (m *ForwardModule) Name() string {
	return "forward"
}

func (m *ForwardModule) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *ForwardModule) ServicesNames() []string {
	return []string{"forwardpb.ForwardService"}
}

func (m *ForwardModule) RegisterService(server *grpc.Server) {
	forwardpb.RegisterForwardServiceServer(server, m.forwardService)
}

// Close closes the module.
func (m *ForwardModule) Close() error {
	for numaIdx, agent := range m.agents {
		if err := agent.Close(); err != nil {
			m.log.Warnw("failed to close shared memory agent", zap.Int("numa", numaIdx), zap.Error(err))
		}
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
