package vxlan

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	vxlanpb "github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// Option configures the DeviceVxlanDevice constructor.
type Option = modulebase.Option

// WithLog sets the logger for the vxlan device.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// DeviceVxlanDevice is the control-plane component of the vxlan device.
type DeviceVxlanDevice struct {
	*modulebase.Base

	cfg     *Config
	service *DeviceVxlanService
}

// NewDeviceVxlanDevice creates a new DeviceVxlanDevice.
func NewDeviceVxlanDevice(cfg *Config, options ...Option) (*DeviceVxlanDevice, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, "vxlan", cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	service := NewDeviceVxlanService(NewBackend(base.Agent()))
	base.AddService(vxlanpb.DeviceVxlanService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		vxlanpb.RegisterDeviceVxlanServiceServer(server, service)
	})

	return &DeviceVxlanDevice{Base: base, cfg: cfg, service: service}, nil
}

// DeviceType returns the registry type of the devices this module owns.
func (m *DeviceVxlanDevice) DeviceType() string {
	return "vxlan"
}

// Dataplane returns the shared-memory path and instance this module's
// agent serves.
func (m *DeviceVxlanDevice) Dataplane() (string, uint32) {
	return m.cfg.MemoryPath.Unwrap(), m.cfg.InstanceID.Unwrap()
}

// DeleteDevice releases the device service's handle for the named device;
// the Device service calls it as it deletes the device.
func (m *DeviceVxlanDevice) DeleteDevice(name string) error {
	return m.service.DeleteDevice(name)
}
