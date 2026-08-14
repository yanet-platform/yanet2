// Package vxlan implements the control plane of the vxlan device.
package vxlan

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

const (
	agentName   = "vxlan"
	deviceName  = "vxlan"
	serviceName = "devices.vxlan.controlplane.vxlanpb.v1.DeviceVxlanService"
)

// Option configures the DeviceVxlanDevice constructor.
type Option func(*deviceOptions)

type deviceOptions struct {
	Log *zap.Logger
}

func newDeviceOptions() *deviceOptions {
	return &deviceOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the vxlan device.
func WithLog(log *zap.Logger) Option {
	return func(o *deviceOptions) {
		o.Log = log
	}
}

// DeviceVxlanDevice is the control-plane component of the vxlan device.
type DeviceVxlanDevice struct {
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *DeviceVxlanService
}

// NewDeviceVxlanDevice creates a new vxlan device instance.
func NewDeviceVxlanDevice(cfg *Config, options ...Option) (*DeviceVxlanDevice, error) {
	opts := newDeviceOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("device", deviceName))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, err
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID.Unwrap()),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID.Unwrap(), cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to attach agent to shared memory: %w", err),
			shm.Detach(),
		)
	}

	return &DeviceVxlanDevice{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: NewDeviceVxlanService(agent),
	}, nil
}

// Name returns the device name.
func (m *DeviceVxlanDevice) Name() string {
	return deviceName
}

// Endpoint returns the gRPC endpoint of the vxlan device.
func (m *DeviceVxlanDevice) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the device.
func (m *DeviceVxlanDevice) ServicesNames() []string {
	return []string{serviceName}
}

// RegisterService registers the vxlan device's gRPC service.
func (m *DeviceVxlanDevice) RegisterService(server *grpc.Server) {
	vxlanpb.RegisterDeviceVxlanServiceServer(server, m.service)
}

// Close closes the device and releases all resources.
func (m *DeviceVxlanDevice) Close() error {
	return errors.Join(m.agent.Close(), m.shm.Detach())
}
