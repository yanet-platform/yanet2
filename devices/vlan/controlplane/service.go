package vlan

import (
	"context"
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb"
)

// DeviceVlanService implements the DeviceVlan gRPC service.
type DeviceVlanService struct {
	vlanpb.UnimplementedDeviceVlanServiceServer

	agents []*ffi.Agent
}

func NewDeviceVlanService(agents []*ffi.Agent) *DeviceVlanService {
	return &DeviceVlanService{
		agents: agents,
	}
}

func (m *DeviceVlanService) UpdateDevice(
	ctx context.Context,
	request *vlanpb.UpdateDeviceVlanRequest,
) (*vlanpb.UpdateDeviceVlanResponse, error) {
	name, instance, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	agent := m.agents[instance]

	deviceConfig, err := NewDeviceConfig(agent, name, request.GetDevice(), uint16(request.GetVlan()))
	if err != nil {
		return nil, fmt.Errorf("failed to create device config for instance %d: %w", instance, err)
	}

	if err := agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		return nil, fmt.Errorf("failed to update module on instance %d: %w", instance, err)
	}

	return nil, nil
}
