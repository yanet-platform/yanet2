package vlan

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// DeviceVlanService implements the DeviceVlan gRPC service.
type DeviceVlanService struct {
	vlanpb.UnimplementedDeviceVlanServiceServer

	mu      sync.Mutex
	agent   *ffi.Agent
	configs map[string]DeviceConfig
}

func NewDeviceVlanService(agent *ffi.Agent) *DeviceVlanService {
	return &DeviceVlanService{
		agent:   agent,
		configs: map[string]DeviceConfig{},
	}
}

func (m *DeviceVlanService) UpdateDevice(
	ctx context.Context,
	request *vlanpb.UpdateDeviceVlanRequest,
) (*vlanpb.UpdateDeviceVlanResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice(), uint16(request.GetVlan()))
	if err != nil {
		return nil, fmt.Errorf("failed to create device config: %w", err)
	}

	if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		deviceConfig.Free()
		return nil, fmt.Errorf("failed to update device: %w", err)
	}

	// UpdateDevices publishes the new generation and waits for the dataplane
	// to drop the old one, so the superseded device is no longer referenced
	// and can be freed explicitly. This mirrors how the ACL control plane
	// reclaims superseded module configs, instead of relying on a type-blind
	// drain of the agent's unused list.
	if old, ok := m.configs[name]; ok {
		old.Free()
	}
	m.configs[name] = *deviceConfig

	return &vlanpb.UpdateDeviceVlanResponse{}, nil
}
