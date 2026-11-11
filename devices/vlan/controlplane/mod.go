package vlan

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// Option configures the DeviceVlanDevice constructor.
type Option = modulebase.Option

// WithLog sets the logger for the vlan device.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// DeviceVlanDevice is the control-plane component of the vlan device.
type DeviceVlanDevice struct {
	*modulebase.Base

	cfg     *Config
	service *DeviceVlanService
}

// NewDeviceVlanDevice creates a new DeviceVlanDevice.
func NewDeviceVlanDevice(cfg *Config, options ...Option) (*DeviceVlanDevice, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, "vlan", cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	service := NewDeviceVlanService(NewBackend(base.Agent()))
	base.AddService(vlanpb.DeviceVlanService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		vlanpb.RegisterDeviceVlanServiceServer(server, service)
	})

	return &DeviceVlanDevice{Base: base, cfg: cfg, service: service}, nil
}

// DeviceType returns the registry type of the devices this module owns.
func (m *DeviceVlanDevice) DeviceType() string {
	return "vlan"
}

// Dataplane returns the shared-memory path and instance this module's
// agent serves.
func (m *DeviceVlanDevice) Dataplane() (string, uint32) {
	return m.cfg.MemoryPath.Unwrap(), m.cfg.InstanceID.Unwrap()
}

// DeleteDevice releases the device service's handle for the named device;
// the Device service calls it as it deletes the device.
func (m *DeviceVlanDevice) DeleteDevice(name string) error {
	return m.service.DeleteDevice(name)
}
