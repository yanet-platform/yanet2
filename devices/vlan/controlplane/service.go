package vlan

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// DevicePipelines is the published payload: the input and output
// pipeline bindings of the device together with its vlan id.
type DevicePipelines struct {
	Input  []commonpb.Pipeline
	Output []commonpb.Pipeline
	VlanID uint16
}

// Backend abstracts shared memory operations.
type Backend interface {
	// Update publishes a device config with the given pipelines and
	// vlan id to the dataplane.
	Update(name string, payload DevicePipelines) (crud.Handle, error)
	// Delete removes the named device from the dataplane registry.
	Delete(name string) error
	// Device returns the live registry entry of the named device.
	Device(name string) (ffi.DeviceInfo, bool)
	// LookupVlan returns the vlan id of the live vlan device with the
	// given name.
	LookupVlan(name string) (uint16, error)
}

// DeviceVlanService implements the DeviceVlan gRPC service.
type DeviceVlanService struct {
	vlanpb.UnimplementedDeviceVlanServiceServer

	backend Backend
	core    *crud.Core[DevicePipelines]
}

func NewDeviceVlanService(backend Backend) *DeviceVlanService {
	return &DeviceVlanService{
		backend: backend,
		core:    crud.NewCore(backend),
	}
}

func (m *DeviceVlanService) UpdateDevice(
	ctx context.Context,
	request *vlanpb.UpdateDeviceVlanRequest,
) (*vlanpb.UpdateDeviceVlanResponse, error) {
	name := request.GetName()
	pipelines := DevicePipelines{
		Input:  commonpb.PipelinesFromProto(request.GetDevice().GetInput()),
		Output: commonpb.PipelinesFromProto(request.GetDevice().GetOutput()),
		VlanID: uint16(request.GetVlan()),
	}

	if err := m.core.Replace(name, pipelines); err != nil {
		return nil, crud.UpdateError(err)
	}

	return &vlanpb.UpdateDeviceVlanResponse{}, nil
}

// DeleteDevice unpublishes the named device, removing it from the
// dataplane registry, and releases the handle kept for it.
//
// The registry delete runs before the entry is withdrawn, so a delete the
// dataplane refuses leaves the published device and its handle in place.
func (m *DeviceVlanService) DeleteDevice(name string) error {
	return m.core.Delete(name)
}

// ShowDevice returns the pipeline bindings and vlan id of the vlan device
// with the given name, read from the dataplane's live device registry
// rather than this service's in-memory state.
func (m *DeviceVlanService) ShowDevice(
	ctx context.Context,
	request *vlanpb.ShowDeviceVlanRequest,
) (*vlanpb.ShowDeviceVlanResponse, error) {
	name := request.GetName()
	vlan, err := m.backend.LookupVlan(name)
	if err != nil {
		if errors.Is(err, ffi.ErrNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	// The bindings come from a second registry read, so a device deleted
	// in between reports not found and one replaced in between pairs the
	// id above with the replacement's bindings.
	info, ok := m.backend.Device(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "vlan device '%s' not found", name)
	}

	return &vlanpb.ShowDeviceVlanResponse{Device: crud.DeviceProto(info), Vlan: uint32(vlan)}, nil
}
