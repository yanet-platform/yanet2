package vlan

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// maxVlanID is the highest VLAN id the dataplane accepts.
//
// 802.1Q reserves VID 4095. The dataplane also writes the id straight
// into the tag's 16-bit TCI unmasked, so anything above 4095 would spill
// into the adjacent PCP/DEI bits too.
const maxVlanID = 4094

// DeviceVlanService implements the DeviceVlan gRPC service.
type DeviceVlanService struct {
	vlanpb.UnimplementedDeviceVlanServiceServer

	mu sync.Mutex
	// deferred holds superseded devices whose free was refused because
	// a live configuration generation still referenced them. This
	// service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []DeviceConfig
	agent    *ffi.Agent
	configs  map[string]DeviceConfig
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
	if err := ffi.ValidateDeviceName(name); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := request.GetDevice().Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	vlan := request.GetVlan()
	if vlan > maxVlanID {
		return nil, status.Errorf(codes.InvalidArgument, "vlan %d exceeds maximum allowed value %d", vlan, maxVlanID)
	}

	deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice(), uint16(vlan))
	if err != nil {
		return nil, fmt.Errorf("failed to create device config: %w", err)
	}

	if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		if err := deviceConfig.Free(); err != nil {
			return nil, fmt.Errorf("failed to update device and free the unpublished replacement: %w (update error: %v)", err, err)
		}
		code := codes.Internal
		if errors.Is(err, ffi.ErrFailedPrecondition) {
			// The device names an entity of the graph it runs that the
			// configuration cannot resolve.
			code = codes.FailedPrecondition
		}
		return nil, status.Errorf(code, "failed to update device: %v", err)
	}

	// The update retired the generations holding this service's deferred
	// devices; retry them, then retire the displaced one: freed outright
	// when dangling, parked while a pinned generation still references
	// it.
	m.reclaimDeferred()
	if old, ok := m.configs[name]; ok {
		m.parkOrFree(old)
	}
	m.configs[name] = *deviceConfig

	return &vlanpb.UpdateDeviceVlanResponse{}, nil
}

// ShowDevice returns the pipeline bindings and vlan id of the vlan device
// with the given name, read from the dataplane's live device registry
// rather than this service's in-memory state.
func (m *DeviceVlanService) ShowDevice(
	ctx context.Context,
	request *vlanpb.ShowDeviceVlanRequest,
) (*vlanpb.ShowDeviceVlanResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "device name is required")
	}
	if err := ffi.ValidateDeviceName(name); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	vlan, err := LookupVlan(m.agent, name)
	if err != nil {
		if errors.Is(err, ffi.ErrNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	// The bindings come from a second registry read, so a device deleted
	// in between reports not found and one replaced in between pairs the
	// id above with the replacement's bindings.
	info, ok := m.agent.DPConfig().Device("vlan", name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "vlan device '%s' not found", name)
	}

	return &vlanpb.ShowDeviceVlanResponse{Device: deviceProto(info), Vlan: uint32(vlan)}, nil
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

// parkOrFree frees the device when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.mu.
func (m *DeviceVlanService) parkOrFree(handle DeviceConfig) {
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every deferred device, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this service's superseded devices; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *DeviceVlanService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *DeviceVlanService) reclaimDeferred() {
	kept := m.deferred[:0]
	for idx := range m.deferred {
		if err := m.deferred[idx].Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, m.deferred[idx])
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
