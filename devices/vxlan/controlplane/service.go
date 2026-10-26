package vxlan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vxlan/bindings/go/cvxlan"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// Pipeline is a weighted input/output pipeline assignment for the device.
type Pipeline struct {
	Name   string
	Weight uint64
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateDevice publishes a device config with the given pipelines and
	// tunnel settings to the dataplane.
	//
	// It returns the freshly published handle. The caller owns the handle
	// and must Free it once a newer generation has superseded it.
	UpdateDevice(name string, input, output []Pipeline, settings cvxlan.Settings) (*cvxlan.DeviceConfig, error)
	// DeleteDevice removes the named device from the dataplane registry.
	DeleteDevice(name string) error
}

// DeviceVxlanService implements the DeviceVxlan gRPC service.
type DeviceVxlanService struct {
	vxlanpb.UnimplementedDeviceVxlanServiceServer

	backend Backend
	configs *configstore.Store[*DeviceEntry]
}

// DeviceEntry is the last applied configuration of one device: the
// shared-memory device owned by the store, and the request fields served
// back by GetDevice.
type DeviceEntry struct {
	handle   *cvxlan.DeviceConfig
	device   *commonpb.Device
	settings cvxlan.Settings
}

// Free releases the shared-memory device the entry owns, reporting
// ffi.ErrStillReferenced while a live generation still holds it.
func (m *DeviceEntry) Free() error {
	return m.handle.Free()
}

// GetResponse renders the entry as a GetDevice reply. The entry never
// changes after it is published, so an unlocked reader sees one applied
// generation whole.
func (m *DeviceEntry) GetResponse(name string) *vxlanpb.GetDeviceVxlanResponse {
	return &vxlanpb.GetDeviceVxlanResponse{
		Name:    name,
		Device:  m.device,
		SrcMac:  net.HardwareAddr(m.settings.SrcMAC[:]).String(),
		DstMac:  net.HardwareAddr(m.settings.DstMAC[:]).String(),
		Vni:     m.settings.VNI,
		DstPort: uint32(m.settings.DstPort),
		SrcIp:   m.settings.SrcIP.String(),
		DstIp:   m.settings.DstIP.String(),
	}
}

// NewDeviceVxlanService creates a service publishing devices through the
// given backend.
func NewDeviceVxlanService(backend Backend) *DeviceVxlanService {
	return &DeviceVxlanService{
		backend: backend,
		configs: configstore.NewStore[*DeviceEntry](),
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
	input := pipelinesFromProto(request.GetDevice().GetInput())
	output := pipelinesFromProto(request.GetDevice().GetOutput())

	err = m.configs.Update(name, func(*DeviceEntry, bool) (*DeviceEntry, error) {
		handle, err := m.backend.UpdateDevice(name, input, output, settings)
		if err != nil {
			code := codes.Internal
			if errors.Is(err, ffi.ErrFailedPrecondition) {
				// The device names an entity of the graph it runs that the
				// configuration cannot resolve.
				code = codes.FailedPrecondition
			}
			return nil, status.Errorf(code, "failed to update device: %v", err)
		}

		return &DeviceEntry{
			handle:   handle,
			device:   copyDevice(request.GetDevice()),
			settings: settings,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	return &vxlanpb.UpdateDeviceVxlanResponse{}, nil
}

// DeleteDevice unpublishes the named device, removing it from the
// dataplane registry, and releases the handle kept for it.
//
// The registry delete runs before the entry is withdrawn, so a delete the
// dataplane refuses leaves the published device and its handle in place.
func (m *DeviceVxlanService) DeleteDevice(name string) error {
	return m.configs.Delete(name, func(*DeviceEntry) error {
		return m.backend.DeleteDevice(name)
	})
}

// GetDevice returns the last applied configuration of the named device.
func (m *DeviceVxlanService) GetDevice(
	ctx context.Context,
	request *vxlanpb.GetDeviceVxlanRequest,
) (*vxlanpb.GetDeviceVxlanResponse, error) {
	cached, ok := m.configs.Get(request.GetName())
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	return cached.GetResponse(request.GetName()), nil
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
