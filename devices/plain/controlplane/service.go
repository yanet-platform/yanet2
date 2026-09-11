package plain

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// DevicePlainService implements the DevicePlain gRPC service.
type DevicePlainService struct {
	plainpb.UnimplementedDevicePlainServiceServer

	agent   *ffi.Agent
	configs *configstore.Store[*DeviceConfig]
}

func NewDevicePlainService(agent *ffi.Agent) *DevicePlainService {
	return &DevicePlainService{
		agent:   agent,
		configs: configstore.NewStore[*DeviceConfig](),
	}
}

func (m *DevicePlainService) UpdateDevice(
	ctx context.Context,
	request *plainpb.UpdateDevicePlainRequest,
) (*plainpb.UpdateDevicePlainResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}
	if err := ffi.ValidateDeviceName(name); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := request.GetDevice().Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	err := m.configs.Update(name, func(*DeviceConfig, bool) (*DeviceConfig, error) {
		deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice())
		if err != nil {
			return nil, fmt.Errorf("failed to create device config: %w", err)
		}

		if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
			if freeErr := deviceConfig.Free(); freeErr != nil {
				return nil, fmt.Errorf("failed to update device and free the unpublished replacement: %w (update error: %v)", freeErr, err)
			}
			code := codes.Internal
			if errors.Is(err, ffi.ErrFailedPrecondition) {
				// The device names an entity of the graph it runs that the
				// configuration cannot resolve.
				code = codes.FailedPrecondition
			}
			return nil, status.Errorf(code, "failed to update device: %v", err)
		}

		return deviceConfig, nil
	})
	if err != nil {
		return nil, err
	}

	return &plainpb.UpdateDevicePlainResponse{}, nil
}

// ShowDevice returns the pipeline bindings of the plain device with the
// given name, read from the dataplane's live device registry rather than
// this service's in-memory state.
func (m *DevicePlainService) ShowDevice(
	ctx context.Context,
	request *plainpb.ShowDevicePlainRequest,
) (*plainpb.ShowDevicePlainResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "device name is required")
	}
	if err := ffi.ValidateDeviceName(name); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	info, ok := m.agent.DPConfig().Device("plain", name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "plain device '%s' not found", name)
	}

	return &plainpb.ShowDevicePlainResponse{Device: deviceProto(info)}, nil
}

func deviceProto(info ffi.DeviceInfo) *commonpb.Device {
	device := &commonpb.Device{
		Input:  make([]*commonpb.DevicePipeline, len(info.InputPipelines)),
		Output: make([]*commonpb.DevicePipeline, len(info.OutputPipelines)),
	}
	for idx, pipeline := range info.InputPipelines {
		device.Input[idx] = &commonpb.DevicePipeline{Name: pipeline.Name, Weight: pipeline.Weight}
	}
	for idx, pipeline := range info.OutputPipelines {
		device.Output[idx] = &commonpb.DevicePipeline{Name: pipeline.Name, Weight: pipeline.Weight}
	}
	return device
}

// ReclaimDeferred retries every superseded device whose free was refused,
// releasing the ones whose generations have drained.
//
// The service runs it after each successful publish, and anything else
// may call it at any time.
func (m *DevicePlainService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}
