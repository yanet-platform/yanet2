package plain

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb"
)

// DevicePlainDevice is a control-plane component responsible for plain devices
type DevicePlainDevice struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agents  []*ffi.Agent
	service *DevicePlainService
	log     *zap.SugaredLogger
}

// NewDevicePlainDevice creates a new DevicePlain device instance
func NewDevicePlainDevice(cfg *Config, log *zap.SugaredLogger) (*DevicePlainDevice, error) {
	log = log.With(zap.String("module", "plainpb.DevicePlainService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	instanceIndices := shm.InstanceIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("instances", instanceIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("plain", instanceIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	plainService := NewDevicePlainService(agents, log)

	return &DevicePlainDevice{
		cfg:     cfg,
		shm:     shm,
		agents:  agents,
		service: plainService,
		log:     log,
	}, nil
}

func (m *DevicePlainDevice) Name() string {
	return "plain"
}

func (m *DevicePlainDevice) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *DevicePlainDevice) ServicesNames() []string {
	return []string{"plainpb.DevicePlainService"}
}

func (m *DevicePlainDevice) RegisterService(server *grpc.Server) {
	plainpb.RegisterDevicePlainServiceServer(server, m.service)
}

// Close closes the device and releases all resources
func (m *DevicePlainDevice) Close() error {
	for instance, agent := range m.agents {
		if err := agent.Close(); err != nil {
			m.log.Warnw("failed to close shared memory agent", zap.Int("instance", instance), zap.Error(err))
		}
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warnw("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
