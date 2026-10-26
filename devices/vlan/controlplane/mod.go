package vlan

import (
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
	cfg        *Config
	attachment *ffi.Attachment
	service    *DeviceVlanService
}

// NewDeviceVlanDevice creates a new DeviceVlan device instance
func NewDeviceVlanDevice(cfg *Config, options ...Option) (*DeviceVlanDevice, error) {
	opts := newDeviceOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("module", vlanpb.DeviceVlanService_ServiceDesc.ServiceName))

	attachment, err := ffi.Attach(cfg.AttachConfig, "vlan", log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	vlanService := NewDeviceVlanService(NewBackend(agent))

	return &DeviceVlanDevice{
		cfg:        cfg,
		attachment: attachment,
		service:    vlanService,
	}, nil
}

func (m *DeviceVlanDevice) Name() string {
	return "vlan"
}

func (m *DeviceVlanDevice) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

func (m *DeviceVlanDevice) ServicesNames() []string {
	return []string{vlanpb.DeviceVlanService_ServiceDesc.ServiceName}
}

func (m *DeviceVlanDevice) RegisterService(server *grpc.Server) {
	vlanpb.RegisterDeviceVlanServiceServer(server, m.service)
}

// Close closes the device and releases all resources
func (m *DeviceVlanDevice) Close() error {
	return m.attachment.Close()
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
