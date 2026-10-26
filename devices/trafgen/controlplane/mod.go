// Package trafgen implements the control plane of the traffic generator device.
package trafgen

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	trafgenpb "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
)

const (
	agentName  = "trafgen"
	deviceName = "trafgen"
)

// Option configures the TrafgenDevice constructor.
type Option func(*deviceOptions)

type deviceOptions struct {
	Log *zap.Logger
}

func newDeviceOptions() *deviceOptions {
	return &deviceOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the trafgen device.
func WithLog(log *zap.Logger) Option {
	return func(o *deviceOptions) {
		o.Log = log
	}
}

// TrafgenDevice is the control-plane component of the traffic generator device.
type TrafgenDevice struct {
	cfg        *Config
	attachment *ffi.Attachment
	service    *TrafgenService
}

// NewTrafgenDevice creates a new TrafgenDevice.
func NewTrafgenDevice(cfg *Config, options ...Option) (*TrafgenDevice, error) {
	opts := newDeviceOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("device", deviceName))

	attachment, err := ffi.Attach(cfg.AttachConfig, agentName, log)
	if err != nil {
		return nil, err
	}
	agent := attachment.Agent

	service := NewTrafgenService(NewBackend(agent))

	return &TrafgenDevice{
		cfg:        cfg,
		attachment: attachment,
		service:    service,
	}, nil
}

// Name returns the device name.
func (m *TrafgenDevice) Name() string {
	return deviceName
}

// Endpoint returns the gRPC endpoint for the trafgen device.
func (m *TrafgenDevice) Endpoint() string {
	return m.cfg.Endpoint.Unwrap()
}

// ServicesNames returns the gRPC service names exposed by the device.
func (m *TrafgenDevice) ServicesNames() []string {
	return []string{trafgenpb.TrafgenService_ServiceDesc.ServiceName}
}

// RegisterService registers the trafgen device's gRPC service.
func (m *TrafgenDevice) RegisterService(server *grpc.Server) {
	trafgenpb.RegisterTrafgenServiceServer(server, m.service)
}

// Close releases shared memory resources held by the device.
func (m *TrafgenDevice) Close() error {
	return m.attachment.Close()
}

// DeviceType returns the registry type of the devices this module owns.
func (m *TrafgenDevice) DeviceType() string {
	return "trafgen"
}

// Dataplane returns the shared-memory path and instance this module's
// agent serves.
func (m *TrafgenDevice) Dataplane() (string, uint32) {
	return m.cfg.MemoryPath.Unwrap(), m.cfg.InstanceID.Unwrap()
}

// DeleteDevice releases the device service's handle for the named device;
// the Device service calls it as it deletes the device.
func (m *TrafgenDevice) DeleteDevice(name string) error {
	return m.service.DeleteDevice(name)
}
