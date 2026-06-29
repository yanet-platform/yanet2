package builtin

import (
	"context"

	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Device is an in-process gRPC service for listing dataplane devices.
type Device struct {
	ynpb.UnimplementedDeviceServiceServer

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
