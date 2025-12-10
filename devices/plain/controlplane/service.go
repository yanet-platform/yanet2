package plain

import (
	"context"
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb"
)

// DevicePlainService implements the DevicePlain gRPC service.
type DevicePlainService struct {
	plainpb.UnimplementedDevicePlainServiceServer

	agents []*ffi.Agent
}

func NewDevicePlainService(agents []*ffi.Agent) *DevicePlainService {
	return &DevicePlainService{
		agents: agents,
	}
}

func (m *DevicePlainService) UpdateDevice(
	ctx context.Context,
	request *plainpb.UpdateDevicePlainRequest,
) (*plainpb.UpdateDevicePlainResponse, error) {
	name, instance, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	agent := m.agents[instance]

	deviceConfig, err := NewDeviceConfig(agent, name, request.GetDevice())
	if err != nil {
		return nil, fmt.Errorf("failed to create device config for instance %d: %w", instance, err)
	}

	if err := agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		return nil, fmt.Errorf("failed to update module on instance %d: %w", instance, err)
	}

	return &plainpb.UpdateDevicePlainResponse{}, nil
}
