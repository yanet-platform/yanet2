package plain

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// Option configures the DevicePlainDevice constructor.
type Option = modulebase.Option

// WithLog sets the logger for the plain device.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// DevicePlainDevice is a control-plane component responsible for plain devices
type DevicePlainDevice struct {
	*modulebase.Base

	cfg     *Config
	service *DevicePlainService
}

// NewDevicePlainDevice creates a new DevicePlain device instance
func NewDevicePlainDevice(cfg *Config, options ...Option) (*DevicePlainDevice, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, "plain", cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	service := NewDevicePlainService(NewBackend(base.Agent()))
	base.AddService(plainpb.DevicePlainService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		plainpb.RegisterDevicePlainServiceServer(server, service)
	})

	return &DevicePlainDevice{Base: base, cfg: cfg, service: service}, nil
}

// DeviceType returns the registry type of the devices this module owns.
func (m *DevicePlainDevice) DeviceType() string {
	return "plain"
}

// Dataplane returns the shared-memory path and instance this module's
// agent serves.
func (m *DevicePlainDevice) Dataplane() (string, uint32) {
	return m.cfg.MemoryPath.Unwrap(), m.cfg.InstanceID.Unwrap()
}

// DeleteDevice releases the device service's handle for the named device;
// the Device service calls it as it deletes the device.
func (m *DevicePlainDevice) DeleteDevice(name string) error {
	return m.service.DeleteDevice(name)
}
