// Package trafgen implements the control plane of the traffic generator device.
package trafgen

import (
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/modulebase"
	trafgenpb "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
)

const (
	agentName  = "trafgen"
	deviceName = "trafgen"
)

// Option configures the TrafgenDevice constructor.
type Option = modulebase.Option

// WithLog sets the logger for the trafgen device.
func WithLog(log *zap.Logger) Option {
	return modulebase.WithLog(log)
}

// TrafgenDevice is the control-plane component of the traffic generator device.
type TrafgenDevice struct {
	*modulebase.Base

	cfg     *Config
	service *TrafgenService
}

// NewTrafgenDevice creates a new TrafgenDevice.
func NewTrafgenDevice(cfg *Config, options ...Option) (*TrafgenDevice, error) {
	base, err := modulebase.NewBase(cfg.AttachConfig, agentName, cfg.Endpoint.Unwrap(), options...)
	if err != nil {
		return nil, err
	}

	service := NewTrafgenService(NewBackend(base.Agent()))
	base.AddService(trafgenpb.TrafgenService_ServiceDesc.ServiceName, func(server *grpc.Server) {
		trafgenpb.RegisterTrafgenServiceServer(server, service)
	})

	return &TrafgenDevice{Base: base, cfg: cfg, service: service}, nil
}

// DeviceType returns the registry type of the devices this module owns.
func (m *TrafgenDevice) DeviceType() string {
	return deviceName
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
