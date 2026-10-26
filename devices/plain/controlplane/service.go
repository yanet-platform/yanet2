package plain

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// Pipeline is a weighted input/output pipeline assignment for the device.
type Pipeline struct {
	Name   string
	Weight uint64
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateDevice publishes a device config with the given pipelines to
	// the dataplane.
	//
	// It returns the freshly published handle. The caller owns the handle
	// and must Free it once a newer generation has superseded it.
	UpdateDevice(name string, input, output []Pipeline) (*cplain.DeviceConfig, error)
	// DeleteDevice removes the named device from the dataplane registry.
	DeleteDevice(name string) error
	// Device returns the live registry entry of the named device.
	Device(name string) (ffi.DeviceInfo, bool)
}

// DevicePlainService implements the DevicePlain gRPC service.
type DevicePlainService struct {
	plainpb.UnimplementedDevicePlainServiceServer

	backend Backend
	configs *configstore.Store[*cplain.DeviceConfig]
}

func NewDevicePlainService(backend Backend) *DevicePlainService {
	return &DevicePlainService{
		backend: backend,
		configs: configstore.NewStore[*cplain.DeviceConfig](),
	}
}

func (m *DevicePlainService) UpdateDevice(
	ctx context.Context,
	request *plainpb.UpdateDevicePlainRequest,
) (*plainpb.UpdateDevicePlainResponse, error) {
	name := request.GetName()
	input := pipelinesFromProto(request.GetDevice().GetInput())
	output := pipelinesFromProto(request.GetDevice().GetOutput())

	err := m.configs.Update(name, func(*cplain.DeviceConfig, bool) (*cplain.DeviceConfig, error) {
		deviceConfig, err := m.backend.UpdateDevice(name, input, output)
		if err != nil {
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

// DeleteDevice unpublishes the named device, removing it from the
// dataplane registry, and releases the handle kept for it.
//
// The registry delete runs before the entry is withdrawn, so a delete the
// dataplane refuses leaves the published device and its handle in place.
func (m *DevicePlainService) DeleteDevice(name string) error {
	return m.configs.Delete(name, func(*cplain.DeviceConfig) error {
		return m.backend.DeleteDevice(name)
	})
}

// ShowDevice returns the pipeline bindings of the plain device with the
// given name, read from the dataplane's live device registry rather than
// this service's in-memory state.
func (m *DevicePlainService) ShowDevice(
	ctx context.Context,
	request *plainpb.ShowDevicePlainRequest,
) (*plainpb.ShowDevicePlainResponse, error) {
	name := request.GetName()
	info, ok := m.backend.Device(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "plain device '%s' not found", name)
	}

	return &plainpb.ShowDevicePlainResponse{Device: deviceProto(info)}, nil
}

// pipelinesFromProto converts the wire pipeline assignments into the service
// representation.
func pipelinesFromProto(pipelines []*commonpb.DevicePipeline) []Pipeline {
	out := make([]Pipeline, 0, len(pipelines))
	for _, pipeline := range pipelines {
		out = append(out, Pipeline{
			Name:   pipeline.GetName(),
			Weight: pipeline.GetWeight(),
		})
	}
	return out
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
