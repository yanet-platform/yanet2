package plain

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// Option configures the DevicePlainDevice constructor.
type Option func(*deviceOptions)

type deviceOptions struct {
	Log *zap.Logger
}

func newDeviceOptions() *deviceOptions {
	return &deviceOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the plain device.
func WithLog(log *zap.Logger) Option {
	return func(o *deviceOptions) {
		o.Log = log
	}
}

// DevicePlainDevice is a control-plane component responsible for plain devices
type DevicePlainDevice struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *DevicePlainService
}

// NewDevicePlainDevice creates a new DevicePlain device instance
func NewDevicePlainDevice(cfg *Config, options ...Option) (*DevicePlainDevice, error) {
	opts := newDeviceOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", "devices.plain.controlplane.plainpb.v1.DevicePlainService"))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach("plain", cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			shm.Detach(),
		)
	}

	plainService := NewDevicePlainService(agent)

	return &DevicePlainDevice{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: plainService,
	}, nil
}

func (m *DevicePlainDevice) Name() string {
	return "plain"
}

func (m *DevicePlainDevice) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *DevicePlainDevice) ServicesNames() []string {
	return []string{"devices.plain.controlplane.plainpb.v1.DevicePlainService"}
}

func (m *DevicePlainDevice) RegisterService(server *grpc.Server) {
	plainpb.RegisterDevicePlainServiceServer(server, m.service)
}

// Close closes the device and releases all resources
func (m *DevicePlainDevice) Close() error {
	return errors.Join(m.agent.Close(), m.shm.Detach())
}
