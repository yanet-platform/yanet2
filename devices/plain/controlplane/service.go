package plain

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb"
)

// DevicePlainService implements the DevicePlain gRPC service
type DevicePlainService struct {
	plainpb.UnimplementedDevicePlainServiceServer

	agents []*ffi.Agent
	log    *zap.SugaredLogger
}

func NewDevicePlainService(
	agents []*ffi.Agent,
	log *zap.SugaredLogger,
) *DevicePlainService {
	return &DevicePlainService{
		agents: agents,
		log:    log,
	}
}

func (m *DevicePlainService) UpdateDevice(
	ctx context.Context,
	request *plainpb.UpdateDevicePlainRequest,
) (*plainpb.UpdateDevicePlainResponse, error) {
	name, inst, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	m.log.Debugw("updating configuration",
		zap.String("device", name),
		zap.Uint32("instance", inst),
	)

	agent := m.agents[inst]

	deviceConfig, err := NewDeviceConfig(agent, name, request.GetDevice())
	if err != nil {
		return nil, fmt.Errorf("failed to create device config for instance %d: %w", inst, err)
	}

	if err := agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		return nil, fmt.Errorf("failed to update module on instance %d: %w", inst, err)
	}

	m.log.Debugw("successfully updated device config",
		zap.String("name", name),
		zap.Uint32("instance", inst),
	)

	return nil, nil
}
