package vlan

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb"
)

// DeviceVlanDevice is a control-plane component responsible for vlan devices
type DeviceVlanDevice struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agents  []*ffi.Agent
	service *DeviceVlanService
	log     *zap.SugaredLogger
}

// NewDeviceVlanDevice creates a new DeviceVlan device instance
func NewDeviceVlanDevice(cfg *Config, log *zap.SugaredLogger) (*DeviceVlanDevice, error) {
	log = log.With(zap.String("module", "vlanpb.DeviceVlanService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath)
	if err != nil {
		return nil, err
	}

	instanceIndices := shm.InstanceIndices()
	log.Debugw("mapping shared memory",
		zap.Uint32s("instances", instanceIndices),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agents, err := shm.AgentsAttach("vlan", instanceIndices, uint(cfg.MemoryRequirements))
	if err != nil {
		return nil, err
	}

	vlanService := NewDeviceVlanService(agents)

	return &DeviceVlanDevice{
		cfg:     cfg,
		shm:     shm,
		agents:  agents,
		service: vlanService,
		log:     log,
	}, nil
}

func (m *DeviceVlanDevice) Name() string {
	return "vlan"
}

func (m *DeviceVlanDevice) Endpoint() string {
	return m.cfg.Endpoint
}

func (m *DeviceVlanDevice) ServicesNames() []string {
	return []string{"vlanpb.DeviceVlanService"}
}

func (m *DeviceVlanDevice) RegisterService(server *grpc.Server) {
	vlanpb.RegisterDeviceVlanServiceServer(server, m.service)
}

// Close closes the device and releases all resources
func (m *DeviceVlanDevice) Close() error {
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
