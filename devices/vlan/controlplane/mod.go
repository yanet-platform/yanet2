package vlan

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// Option configures the DeviceVlanDevice constructor.
type Option func(*deviceOptions)

type deviceOptions struct {
	Log *zap.Logger
}

func newDeviceOptions() *deviceOptions {
	return &deviceOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the vlan device.
func WithLog(log *zap.Logger) Option {
	return func(o *deviceOptions) {
		o.Log = log
	}
}

// DeviceVlanDevice is a control-plane component responsible for vlan devices
type DeviceVlanDevice struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *DeviceVlanService
	log     *zap.Logger
}

// NewDeviceVlanDevice creates a new DeviceVlan device instance
func NewDeviceVlanDevice(cfg *Config, options ...Option) (*DeviceVlanDevice, error) {
	opts := newDeviceOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "devices.vlan.controlplane.vlanpb.v1.DeviceVlanService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("vlan", cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	vlanService := NewDeviceVlanService(agent)

	return &DeviceVlanDevice{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: vlanService,
		log:     log,
	}, nil
}

func (m *DeviceVlanDevice) Name() string {
	return "vlan"
}

func (m *DeviceVlanDevice) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *DeviceVlanDevice) ServicesNames() []string {
	return []string{"devices.vlan.controlplane.vlanpb.v1.DeviceVlanService"}
}

func (m *DeviceVlanDevice) RegisterService(server *grpc.Server) {
	vlanpb.RegisterDeviceVlanServiceServer(server, m.service)
}

// Close closes the device and releases all resources
func (m *DeviceVlanDevice) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
