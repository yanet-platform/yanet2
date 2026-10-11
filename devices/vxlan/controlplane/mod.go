// Package vxlan implements the control plane of the vxlan device.
package vxlan

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
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

// DeviceVxlanDevice is a control-plane component responsible for vxlan
// devices
type DeviceVxlanDevice struct {
	cfg        *Config
	attachment *ffi.Attachment
	service    *DeviceVxlanService
}

const deviceName = "vxlan"

// NewDeviceVxlanDevice creates a new DeviceVxlan device instance
func NewDeviceVxlanDevice(cfg *Config, options ...Option) (*DeviceVxlanDevice, error) {
	opts := newDeviceOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("device", deviceName))

	attachment, err := ffi.Attach(cfg.AttachConfig, "vxlan", log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	return &DeviceVxlanDevice{
		cfg:        cfg,
		attachment: attachment,
		service:    NewDeviceVxlanService(agent),
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
	return []string{vxlanpb.DeviceVxlanService_ServiceDesc.ServiceName}
}

// RegisterService registers the vxlan device's gRPC service.
func (m *DeviceVxlanDevice) RegisterService(server *grpc.Server) {
	vxlanpb.RegisterDeviceVxlanServiceServer(server, m.service)
}

// Close closes the device and releases all resources
func (m *DeviceVxlanDevice) Close() error {
	return m.attachment.Close()
}
