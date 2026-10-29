package vxlan

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/devices/vxlan/bindings/go/cvxlan"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// DeviceState is the published payload: the last applied configuration
// of one device — its pipeline bindings, tunnel settings, and the
// request fields served back by GetDevice.
type DeviceState struct {
	Input    []commonpb.Pipeline
	Output   []commonpb.Pipeline
	Settings cvxlan.Settings
	Device   *commonpb.Device
}

// Backend abstracts shared memory operations.
type Backend interface {
	// Update publishes a device config with the given pipelines and
	// tunnel settings to the dataplane.
	Update(name string, payload DeviceState) (crud.Handle, error)
	// Delete removes the named device from the dataplane registry.
	Delete(name string) error
}

// DeviceVxlanService implements the DeviceVxlan gRPC service.
type DeviceVxlanService struct {
	vxlanpb.UnimplementedDeviceVxlanServiceServer

	core *crud.Core[DeviceState]
}

// NewDeviceVxlanService creates a service publishing devices through the
// given backend.
func NewDeviceVxlanService(backend Backend) *DeviceVxlanService {
	return &DeviceVxlanService{
		core: crud.NewCore(backend),
	}
}

// UpdateDevice installs the pipelines and tunnel parameters of one vxlan
// device.
func (m *DeviceVxlanService) UpdateDevice(
	ctx context.Context,
	request *vxlanpb.UpdateDeviceVxlanRequest,
) (*vxlanpb.UpdateDeviceVxlanResponse, error) {
	name := request.GetName()
	settings, err := newSettings(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	state := DeviceState{
		Input:    commonpb.PipelinesFromProto(request.GetDevice().GetInput()),
		Output:   commonpb.PipelinesFromProto(request.GetDevice().GetOutput()),
		Settings: settings,
		Device:   copyDevice(request.GetDevice()),
	}

	if err := m.core.Replace(name, state); err != nil {
		return nil, crud.UpdateError(err)
	}

	return &vxlanpb.UpdateDeviceVxlanResponse{}, nil
}

// DeleteDevice unpublishes the named device, removing it from the
// dataplane registry, and releases the handle kept for it.
//
// The registry delete runs before the entry is withdrawn, so a delete the
// dataplane refuses leaves the published device and its handle in place.
func (m *DeviceVxlanService) DeleteDevice(name string) error {
	return m.core.Delete(name)
}

// GetDevice returns the last applied configuration of the named device.
func (m *DeviceVxlanService) GetDevice(
	ctx context.Context,
	request *vxlanpb.GetDeviceVxlanRequest,
) (*vxlanpb.GetDeviceVxlanResponse, error) {
	state, ok := m.core.Get(request.GetName())
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	return state.GetResponse(request.GetName()), nil
}

// GetResponse renders the state as a GetDevice reply. The state never
// changes after it is published, so an unlocked reader sees one applied
// generation whole.
func (m *DeviceState) GetResponse(name string) *vxlanpb.GetDeviceVxlanResponse {
	return &vxlanpb.GetDeviceVxlanResponse{
		Name:    name,
		Device:  m.Device,
		SrcMac:  net.HardwareAddr(m.Settings.SrcMAC[:]).String(),
		DstMac:  net.HardwareAddr(m.Settings.DstMAC[:]).String(),
		Vni:     m.Settings.VNI,
		DstPort: uint32(m.Settings.DstPort),
		SrcIp:   m.Settings.SrcIP.String(),
		DstIp:   m.Settings.DstIP.String(),
	}
}

// copyDevice deep-copies a request's pipeline bindings, so a reused or
// mutated request cannot change what GetDevice serves.
func copyDevice(device *commonpb.Device) *commonpb.Device {
	if device == nil {
		return nil
	}

	return proto.Clone(device).(*commonpb.Device)
}

// newSettings validates the tunnel fields of a request and converts them
// into the Settings representation.
//
// Every field is checked before any shared-memory call, so an invalid
// request never reaches shared memory.
func newSettings(request *vxlanpb.UpdateDeviceVxlanRequest) (cvxlan.Settings, error) {
	vni := request.GetVni()
	if vni > 0xFFFFFF {
		return cvxlan.Settings{}, fmt.Errorf("vni %d exceeds the 24-bit limit", vni)
	}

	dstPort := request.GetDstPort()
	if dstPort == 0 || dstPort > 0xFFFF {
		return cvxlan.Settings{}, fmt.Errorf("destination port %d must be in 1..65535", dstPort)
	}

	srcMAC, err := parseMAC(request.GetSrcMac())
	if err != nil {
		return cvxlan.Settings{}, fmt.Errorf("invalid source mac: %w", err)
	}
	dstMAC, err := parseMAC(request.GetDstMac())
	if err != nil {
		return cvxlan.Settings{}, fmt.Errorf("invalid destination mac: %w", err)
	}

	srcIP, err := netip.ParseAddr(request.GetSrcIp())
	if err != nil {
		return cvxlan.Settings{}, fmt.Errorf("invalid source ip: %w", err)
	}
	if !srcIP.Is4() {
		return cvxlan.Settings{}, fmt.Errorf("source ip %q must be an IPv4 address", request.GetSrcIp())
	}

	dstIP, err := netip.ParseAddr(request.GetDstIp())
	if err != nil {
		return cvxlan.Settings{}, fmt.Errorf("invalid destination ip: %w", err)
	}
	if !dstIP.Is4() {
		return cvxlan.Settings{}, fmt.Errorf("destination ip %q must be an IPv4 address", request.GetDstIp())
	}

	return cvxlan.Settings{
		VNI:     vni,
		DstPort: uint16(dstPort),
		SrcMAC:  srcMAC,
		DstMAC:  dstMAC,
		SrcIP:   srcIP,
		DstIP:   dstIP,
	}, nil
}

// parseMAC parses a colon-separated MAC address into exactly six wire-order
// bytes, rejecting the longer EUI-64 forms net.ParseMAC also accepts.
func parseMAC(value string) ([6]byte, error) {
	address, err := net.ParseMAC(value)
	if err != nil {
		return [6]byte{}, err
	}
	if len(address) != 6 {
		return [6]byte{}, fmt.Errorf("mac %q is not a 6-byte ethernet address", value)
	}

	var mac [6]byte
	copy(mac[:], address)
	return mac, nil
}
