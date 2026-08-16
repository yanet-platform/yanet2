package plain

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// DevicePlainService implements the DevicePlain gRPC service.
type DevicePlainService struct {
	plainpb.UnimplementedDevicePlainServiceServer

	mu      sync.Mutex
	agent   *ffi.Agent
	configs map[string]DeviceConfig
}

func NewDevicePlainService(agent *ffi.Agent) *DevicePlainService {
	return &DevicePlainService{
		agent:   agent,
		configs: map[string]DeviceConfig{},
	}
}

func (m *DevicePlainService) UpdateDevice(
	ctx context.Context,
	request *plainpb.UpdateDevicePlainRequest,
) (*plainpb.UpdateDevicePlainResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice())
	if err != nil {
		return nil, fmt.Errorf("failed to create device config: %w", err)
	}

	if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		deviceConfig.Free()
		return nil, fmt.Errorf("failed to update device: %w", err)
	}

	// Free only drops the superseded handle's construction reference: the
	// device parks on the agent, and the next plain-device construction
	// reclaims it. This mirrors how the module control planes retire
	// superseded configs.
	if old, ok := m.configs[name]; ok {
		old.Free()
	}
	m.configs[name] = *deviceConfig

	return &plainpb.UpdateDevicePlainResponse{}, nil
}
