package builtin

import (
	"context"
	"errors"
	"sync"

	"github.com/c2h5oh/datasize"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

const deviceAgentName = "device"

// A registry delete the Device service runs itself attaches a fresh
// agent whose 1MB covers the delete's temporary allocations.
//
// The arena persists until a later attach under the same name reclaims
// it.
const deviceAgentMemory = datasize.MB

// DeviceOwner is the in-process control plane of one device kind, keeping
// a handle for every device it has published.
//
// An owner serves the same shared-memory dataplane as the Device service
// that routes deletes to it.
type DeviceOwner interface {
	// DeviceType returns the registry type of the devices it owns, such
	// as "plain".
	DeviceType() string
	// Dataplane returns the shared-memory path and instance the owner's
	// agents serve.
	Dataplane() (memoryPath string, instanceID uint32)
	// DeleteDevice unpublishes the named device, removing it from the
	// dataplane registry, and releases the handle kept for it.
	//
	// It reports configstore.ErrNotFound when nothing is published under
	// the name.
	DeleteDevice(name string) error
}

// Device is an in-process gRPC service for listing and deleting dataplane
// devices.
type Device struct {
	ynpb.UnimplementedDeviceServiceServer

	// mu serializes the attach-through-delete lifetime: a concurrent
	// attach under the same agent name reclaims the agent still in use.
	mu sync.Mutex

	instanceID uint32
	shm        *ffi.SharedMemory

	// owners maps a registry device type to the control plane that
	// publishes devices of the type.
	owners map[string]DeviceOwner
}

// NewDevice creates a new Device service.
//
// Every registered owner reclaims the handles of its devices as they are
// deleted; a device whose type has no owner is removed from the registry
// alone. An owner serving another dataplane than the one named here is
// not registered: its devices are not this service's to delete.
func NewDevice(
	instanceID uint32,
	shm *ffi.SharedMemory,
	memoryPath string,
	owners ...DeviceOwner,
) *Device {
	ownersByType := map[string]DeviceOwner{}
	for _, owner := range owners {
		ownerPath, ownerInstance := owner.Dataplane()
		if ownerPath != memoryPath || ownerInstance != instanceID {
			continue
		}
		ownersByType[owner.DeviceType()] = owner
	}

	return &Device{
		instanceID: instanceID,
		shm:        shm,
		owners:     ownersByType,
	}
}

// Name returns the service name.
func (m *Device) Name() string { return "device" }

// Endpoint returns empty string indicating in-process service.
func (m *Device) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Device) ServicesNames() []string {
	return []string{ynpb.DeviceService_ServiceDesc.ServiceName}
}

// RegisterService registers the service on the given gRPC server.
func (m *Device) RegisterService(server *grpc.Server) {
	ynpb.RegisterDeviceServiceServer(server, m)
}

// List returns all configured devices with their registry indices.
func (m *Device) List(
	ctx context.Context,
	request *ynpb.ListDevicesRequest,
) (*ynpb.ListDevicesResponse, error) {
	dpConfig := m.shm.DPConfig(m.instanceID)
	devices := dpConfig.Devices()

	ids := make([]*ynpb.DeviceId, len(devices))
	for idx, device := range devices {
		ids[idx] = &ynpb.DeviceId{
			Type:  device.Type,
			Name:  device.Name,
			Index: device.Index,
		}
	}

	return &ynpb.ListDevicesResponse{Ids: ids}, nil
}

// Delete removes a device by name. A predefined topology device is
// refused and an absent name is reported as not found.
//
// A device whose type has a registered owner is deleted through it, so
// the owner's handle for the name is released with the registry entry.
// The owner runs its registry delete under its per-name writer lock, so
// the delete cannot interleave with a concurrent update of the same name.
func (m *Device) Delete(
	ctx context.Context,
	request *ynpb.DeleteDeviceRequest,
) (*ynpb.DeleteDeviceResponse, error) {
	name := request.GetName()
	m.mu.Lock()
	defer m.mu.Unlock()

	if owner, ok := m.ownerOf(name); ok {
		switch err := owner.DeleteDevice(name); {
		case err == nil:
			return &ynpb.DeleteDeviceResponse{}, nil
		case errors.Is(err, configstore.ErrNotFound):
			// The owner never published the name: the device predates
			// this process or was created beside it, so only the
			// registry entry is left to remove.
		default:
			return nil, deleteError(err)
		}
	}

	agent, err := m.shm.AgentAttach(deviceAgentName, m.instanceID, deviceAgentMemory)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer agent.Close()

	if err := agent.DeleteDevice(name); err != nil {
		return nil, deleteError(err)
	}

	return &ynpb.DeleteDeviceResponse{}, nil
}

// ownerOf returns the registered owner of the named device's type, when
// the device is in the registry and its type has a registered owner.
func (m *Device) ownerOf(name string) (DeviceOwner, bool) {
	for _, device := range m.shm.DPConfig(m.instanceID).Devices() {
		if device.Name != name {
			continue
		}
		owner, ok := m.owners[device.Type]
		return owner, ok
	}

	return nil, false
}

// deleteError turns a dataplane device delete failure into its gRPC
// status.
func deleteError(err error) error {
	switch {
	case errors.Is(err, ffi.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ffi.ErrFailedPrecondition):
		// A predefined topology device cannot be deleted.
		return status.Error(codes.FailedPrecondition, err.Error())
	}

	return status.Error(codes.Internal, err.Error())
}
