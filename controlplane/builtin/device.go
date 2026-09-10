package builtin

import (
	"context"
	"errors"
	"sync"

	"github.com/c2h5oh/datasize"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

const deviceAgentName = "device"

// Each call attaches a fresh agent whose 1MB covers a delete's
// temporary allocations.
//
// The arena persists until a later attach under the same name
// reclaims it.
const deviceAgentMemory = datasize.MB

// Device is an in-process gRPC service for listing and deleting dataplane
// devices.
type Device struct {
	ynpb.UnimplementedDeviceServiceServer

	// mu serializes the attach-through-delete lifetime: a concurrent
	// attach under the same agent name reclaims the agent still in use.
	mu sync.Mutex

	instanceID uint32
	shm        *ffi.SharedMemory
}

// NewDevice creates a new Device service.
func NewDevice(instanceID uint32, shm *ffi.SharedMemory) *Device {
	return &Device{
		instanceID: instanceID,
		shm:        shm,
	}
}

// Name returns the service name.
func (m *Device) Name() string { return "device" }

// Endpoint returns empty string indicating in-process service.
func (m *Device) Endpoint() string { return "" }

// ServicesNames returns the gRPC service names served by this service.
func (m *Device) ServicesNames() []string { return []string{"controlplane.ynpb.v1.DeviceService"} }

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
func (m *Device) Delete(
	ctx context.Context,
	request *ynpb.DeleteDeviceRequest,
) (*ynpb.DeleteDeviceResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "device name is required")
	}
	if err := ffi.ValidateDeviceName(name); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	agent, err := m.shm.AgentAttach(deviceAgentName, m.instanceID, deviceAgentMemory)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer agent.Close()

	if err := agent.DeleteDevice(name); err != nil {
		switch {
		case errors.Is(err, ffi.ErrNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, ffi.ErrInvalidArgument):
			// A predefined topology device cannot be deleted.
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &ynpb.DeleteDeviceResponse{}, nil
}
