package plain

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// DevicePipelines is the published payload: the input and output
// pipeline bindings of the device.
type DevicePipelines struct {
	Input  []commonpb.Pipeline
	Output []commonpb.Pipeline
}

// Backend abstracts shared memory operations.
type Backend interface {
	// Update publishes a device config with the given pipelines to
	// the dataplane.
	Update(name string, payload DevicePipelines) (crud.Handle, error)
	// Delete removes the named device from the dataplane registry.
	Delete(name string) error
	// Device returns the live registry entry of the named device.
	Device(name string) (ffi.DeviceInfo, bool)
}

// DevicePlainService implements the DevicePlain gRPC service.
type DevicePlainService struct {
	plainpb.UnimplementedDevicePlainServiceServer

	backend Backend
	core    *crud.Core[DevicePipelines]
}

func NewDevicePlainService(backend Backend) *DevicePlainService {
	return &DevicePlainService{
		backend: backend,
		core:    crud.NewCore(backend),
	}
}

func (m *DevicePlainService) UpdateDevice(
	ctx context.Context,
	request *plainpb.UpdateDevicePlainRequest,
) (*plainpb.UpdateDevicePlainResponse, error) {
	name := request.GetName()
	pipelines := DevicePipelines{
		Input:  commonpb.PipelinesFromProto(request.GetDevice().GetInput()),
		Output: commonpb.PipelinesFromProto(request.GetDevice().GetOutput()),
	}

	if err := m.core.Replace(name, pipelines); err != nil {
		return nil, crud.UpdateError(err)
	}

	return &plainpb.UpdateDevicePlainResponse{}, nil
}

// DeleteDevice unpublishes the named device, removing it from the
// dataplane registry, and releases the handle kept for it.
//
// The registry delete runs before the entry is withdrawn, so a delete the
// dataplane refuses leaves the published device and its handle in place.
func (m *DevicePlainService) DeleteDevice(name string) error {
	return m.core.Delete(name)
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

	return &plainpb.ShowDevicePlainResponse{Device: crud.DeviceProto(info)}, nil
}
