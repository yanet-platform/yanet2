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

	mu    sync.Mutex
	agent *ffi.Agent
}

func NewDevicePlainService(agent *ffi.Agent) *DevicePlainService {
	return &DevicePlainService{
		agent: agent,
	}
}

func (m *DevicePlainService) UpdateDevice(
	ctx context.Context,
	request *plainpb.UpdateDevicePlainRequest,
) (*plainpb.UpdateDevicePlainResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Reclaim devices parked on the agent's unused list, whatever the
	// outcome of this update.
	//
	// The drain is registered before the device config is created on
	// purpose: once the arena is full, creation fails first, so a drain
	// registered after that check would never run and the arena could
	// never recover.
	defer DrainUnusedDevices(m.agent)

	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice())
	if err != nil {
		return nil, fmt.Errorf("failed to create device config: %w", err)
	}

	if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		return nil, fmt.Errorf("failed to update device: %w", err)
	}

	return &plainpb.UpdateDevicePlainResponse{}, nil
}
