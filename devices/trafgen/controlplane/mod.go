// Package trafgen implements the control plane of the traffic generator device.
package trafgen

import (
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	trafgenpb "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
)

const (
	agentName   = "trafgen"
	deviceName  = "trafgen"
	serviceName = "devices.trafgen.controlplane.trafgenpb.v1.TrafgenService"
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
	cfg     *Config
	shm     *ffi.SharedMemory
	agent   *ffi.Agent
	service *TrafgenService
	log     *zap.Logger
}

// NewTrafgenDevice creates a new TrafgenDevice.
func NewTrafgenDevice(cfg *Config, options ...Option) (*TrafgenDevice, error) {
	opts := newDeviceOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log.With(zap.String("device", deviceName))

	shm, err := ffi.AttachSharedMemory(cfg.MemoryPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	log.Debug("mapping shared memory",
		zap.Uint32("instance_id", cfg.InstanceID),
		zap.Stringer("size", cfg.MemoryRequirements),
	)

	agent, err := shm.AgentAttach(agentName, cfg.InstanceID, cfg.MemoryRequirements.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent to shared memory: %w", err)
	}

	service := NewTrafgenService(NewBackend(agent))

	return &TrafgenDevice{
		cfg:     cfg,
		shm:     shm,
		agent:   agent,
		service: service,
		log:     log,
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
	return []string{serviceName}
}

// RegisterService registers the trafgen device's gRPC service.
func (m *TrafgenDevice) RegisterService(server *grpc.Server) {
	trafgenpb.RegisterTrafgenServiceServer(server, m.service)
}

// Close releases shared memory resources held by the device.
func (m *TrafgenDevice) Close() error {
	if err := m.agent.Close(); err != nil {
		m.log.Warn("failed to close shared memory agent", zap.Error(err))
	}

	if err := m.shm.Detach(); err != nil {
		m.log.Warn("failed to detach from shared memory mapping", zap.Error(err))
	}

	return nil
}
