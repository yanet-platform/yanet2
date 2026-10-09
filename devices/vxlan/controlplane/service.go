package vxlan

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// DeviceVxlanService implements the DeviceVxlan gRPC service.
type DeviceVxlanService struct {
	vxlanpb.UnimplementedDeviceVxlanServiceServer

	agent   *ffi.Agent
	configs *configstore.Store[*DeviceConfig]
}

func NewDeviceVxlanService(agent *ffi.Agent) *DeviceVxlanService {
	return &DeviceVxlanService{
		agent:   agent,
		configs: configstore.NewStore[*DeviceConfig](),
	}
}

// UpdateDevice creates or replaces the vxlan device with the given name.
//
// A dataplane built without the vxlan device type reports
// FailedPrecondition.
func (m *DeviceVxlanService) UpdateDevice(
	ctx context.Context,
	request *vxlanpb.UpdateDeviceVxlanRequest,
) (*vxlanpb.UpdateDeviceVxlanResponse, error) {
	name := request.GetName()
	tunnel := tunnelFromProto(request.GetTunnel())
	err := m.configs.Update(name, func(*DeviceConfig, bool) (*DeviceConfig, error) {
		deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice(), tunnel)
		if err != nil {
			return nil, status.Error(statusCode(err), err.Error())
		}

		if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
			if freeErr := deviceConfig.Free(); freeErr != nil {
				return nil, status.Errorf(
					codes.Internal,
					"failed to update device and free the unpublished replacement: %v (update error: %v)",
					freeErr,
					err,
				)
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

	return &vxlanpb.UpdateDeviceVxlanResponse{}, nil
}

// ShowDevice returns the pipeline bindings and tunnel of the vxlan device
// with the given name, read from the dataplane's live device registry
// rather than this service's in-memory state.
func (m *DeviceVxlanService) ShowDevice(
	ctx context.Context,
	request *vxlanpb.ShowDeviceVxlanRequest,
) (*vxlanpb.ShowDeviceVxlanResponse, error) {
	name := request.GetName()
	tunnel, err := LookupTunnel(m.agent, name)
	if err != nil {
		return nil, status.Error(statusCode(err), err.Error())
	}

	// The bindings come from a second registry read, so the tunnel above
	// may belong to another generation than the bindings.
	//
	// A device deleted in between reports not found; one replaced in
	// between pairs the tunnel with the replacement's bindings.
	info, ok := m.agent.DPConfig().Device(deviceName, name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "vxlan device '%s' not found", name)
	}

	return &vxlanpb.ShowDeviceVxlanResponse{
		Device: deviceProto(info),
		Tunnel: tunnelProto(tunnel),
	}, nil
}

// statusCode maps a failure of the Rust control plane to the gRPC code the
// caller sees.
func statusCode(err error) codes.Code {
	switch {
	case errors.Is(err, ffi.ErrInvalidArgument):
		return codes.InvalidArgument
	case errors.Is(err, ffi.ErrNotFound):
		return codes.NotFound
	case errors.Is(err, ffi.ErrFailedPrecondition):
		return codes.FailedPrecondition
	}

	return codes.Internal
}

func tunnelFromProto(tunnel *vxlanpb.VxlanTunnel) Tunnel {
	return Tunnel{
		LocalMAC:  tunnel.GetLocalMac().EUI48(),
		RemoteMAC: tunnel.GetRemoteMac().EUI48(),
		LocalIP:   tunnel.GetLocalIp().ToAddr().As4(),
		RemoteIP:  tunnel.GetRemoteIp().ToAddr().As4(),
		VNI:       tunnel.GetVni(),
	}
}

func tunnelProto(tunnel Tunnel) *vxlanpb.VxlanTunnel {
	return &vxlanpb.VxlanTunnel{
		LocalIp:   commonpb.NewIPv4Address(tunnel.LocalIP),
		RemoteIp:  commonpb.NewIPv4Address(tunnel.RemoteIP),
		LocalMac:  commonpb.NewMACAddressEUI48(tunnel.LocalMAC),
		RemoteMac: commonpb.NewMACAddressEUI48(tunnel.RemoteMAC),
		Vni:       tunnel.VNI,
	}
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
