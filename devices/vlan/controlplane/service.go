package vlan

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/bindings/go/cvlan"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// Pipeline is a weighted input/output pipeline assignment for the device.
type Pipeline struct {
	Name   string
	Weight uint64
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateDevice publishes a device config with the given pipelines and
	// vlan id to the dataplane.
	//
	// It returns the freshly published handle. The caller owns the handle
	// and must Free it once a newer generation has superseded it.
	UpdateDevice(name string, input, output []Pipeline, vlanID uint16) (*cvlan.DeviceConfig, error)
	// DeleteDevice removes the named device from the dataplane registry.
	DeleteDevice(name string) error
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
	configs *configstore.Store[*cvlan.DeviceConfig]
}

func NewDeviceVlanService(backend Backend) *DeviceVlanService {
	return &DeviceVlanService{
		backend: backend,
		configs: configstore.NewStore[*cvlan.DeviceConfig](),
	}
}

func (m *DeviceVlanService) UpdateDevice(
	ctx context.Context,
	request *vlanpb.UpdateDeviceVlanRequest,
) (*vlanpb.UpdateDeviceVlanResponse, error) {
	name := request.GetName()
	input := pipelinesFromProto(request.GetDevice().GetInput())
	output := pipelinesFromProto(request.GetDevice().GetOutput())
	vlanID := uint16(request.GetVlan())

	err := m.configs.Update(name, func(*cvlan.DeviceConfig, bool) (*cvlan.DeviceConfig, error) {
		deviceConfig, err := m.backend.UpdateDevice(name, input, output, vlanID)
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

	return &vlanpb.UpdateDeviceVlanResponse{}, nil
}

// DeleteDevice unpublishes the named device, removing it from the
// dataplane registry, and releases the handle kept for it.
//
// The registry delete runs before the entry is withdrawn, so a delete the
// dataplane refuses leaves the published device and its handle in place.
func (m *DeviceVlanService) DeleteDevice(name string) error {
	return m.configs.Delete(name, func(*cvlan.DeviceConfig) error {
		return m.backend.DeleteDevice(name)
	})
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

	return &vlanpb.ShowDeviceVlanResponse{Device: deviceProto(info), Vlan: uint32(vlan)}, nil
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
