// Package vxlan implements the control plane of the vxlan device.
package vxlan

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

const (
	agentName  = "vxlan"
	deviceName = "vxlan"
)

// Option configures the vxlan device constructor.
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
	cfg        *Config
	attachment *ffi.Attachment
	service    *DeviceVxlanService
}

// NewDeviceVxlanDevice creates a new vxlan device instance.
//
// It refuses to attach when the linked Rust control-plane archive speaks
// another ABI version than this build.
func NewDeviceVxlanDevice(cfg *Config, options ...Option) (*DeviceVxlanDevice, error) {
	opts := newDeviceOptions()
	for _, o := range options {
		o(opts)
	}

	if err := CheckABI(); err != nil {
		return nil, err
	}

	log := opts.Log.With(zap.String("module", vxlanpb.DeviceVxlanService_ServiceDesc.ServiceName))

	attachment, err := ffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}

	return &DeviceVxlanDevice{
		cfg:        cfg,
		attachment: attachment,
		service:    NewDeviceVxlanService(attachment.Agent),
	}, nil
}

// Name returns the device name.
func (m *DeviceVxlanDevice) Name() string {
	return deviceName
}

// Endpoint returns the gRPC endpoint for the vxlan device.
func (m *DeviceVxlanDevice) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the device.
func (m *DeviceVxlanDevice) ServicesNames() []string {
	return []string{vxlanpb.DeviceVxlanService_ServiceDesc.ServiceName}
}

// RegisterService registers the device's gRPC service on the server.
func (m *DeviceVxlanDevice) RegisterService(server *grpc.Server) {
	vxlanpb.RegisterDeviceVxlanServiceServer(server, m.service)
}

// Close closes the device and releases all resources.
func (m *DeviceVxlanDevice) Close() error {
	return m.attachment.Close()
}
