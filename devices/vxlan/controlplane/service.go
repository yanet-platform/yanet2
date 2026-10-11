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
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// DeviceVxlanService implements the DeviceVxlan gRPC service.
type DeviceVxlanService struct {
	vxlanpb.UnimplementedDeviceVxlanServiceServer

	agent   *ffi.Agent
	configs *configstore.Store[*DeviceEntry]
}

// DeviceEntry is the last applied configuration of one device: the
// shared-memory device owned by the store, and the request fields served
// back by GetDevice.
type DeviceEntry struct {
	config   DeviceConfig
	device   *commonpb.Device
	settings Settings
}

// Free releases the shared-memory device the entry owns, reporting
// ffi.ErrStillReferenced while a live generation still holds it.
func (m *DeviceEntry) Free() error {
	return m.config.Free()
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
// given agent.
func NewDeviceVxlanService(agent *ffi.Agent) *DeviceVxlanService {
	return &DeviceVxlanService{
		agent:   agent,
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

	err = m.configs.Update(name, func(*DeviceEntry, bool) (*DeviceEntry, error) {
		deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice(), settings)
		if err != nil {
			return nil, fmt.Errorf("failed to create device config: %w", err)
		}

		if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
			if freeErr := deviceConfig.Free(); freeErr != nil {
				return nil, fmt.Errorf("failed to update device and free the unpublished replacement: %w (update error: %v)", freeErr, err)
			}
			code := codes.Internal
			if errors.Is(err, ffi.ErrFailedPrecondition) {
				// The device names an entity of the graph it runs that the
				// configuration cannot resolve.
				code = codes.FailedPrecondition
			}
			return nil, status.Errorf(code, "failed to update device: %v", err)
		}

		return &DeviceEntry{
			config:   *deviceConfig,
			device:   copyDevice(request.GetDevice()),
			settings: settings,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	return &vxlanpb.UpdateDeviceVxlanResponse{}, nil
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
// Every field is checked before any CGO call, so an invalid request never
// reaches shared memory.
func newSettings(request *vxlanpb.UpdateDeviceVxlanRequest) (Settings, error) {
	vni := request.GetVni()
	if vni > 0xFFFFFF {
		return Settings{}, fmt.Errorf("vni %d exceeds the 24-bit limit", vni)
	}

	dstPort := request.GetDstPort()
	if dstPort == 0 || dstPort > 0xFFFF {
		return Settings{}, fmt.Errorf("destination port %d must be in 1..65535", dstPort)
	}

	srcMAC, err := parseMAC(request.GetSrcMac())
	if err != nil {
		return Settings{}, fmt.Errorf("invalid source mac: %w", err)
	}
	dstMAC, err := parseMAC(request.GetDstMac())
	if err != nil {
		return Settings{}, fmt.Errorf("invalid destination mac: %w", err)
	}

	srcIP, err := netip.ParseAddr(request.GetSrcIp())
	if err != nil {
		return Settings{}, fmt.Errorf("invalid source ip: %w", err)
	}
	if !srcIP.Is4() {
		return Settings{}, fmt.Errorf("source ip %q must be an IPv4 address", request.GetSrcIp())
	}

	dstIP, err := netip.ParseAddr(request.GetDstIp())
	if err != nil {
		return Settings{}, fmt.Errorf("invalid destination ip: %w", err)
	}
	if !dstIP.Is4() {
		return Settings{}, fmt.Errorf("destination ip %q must be an IPv4 address", request.GetDstIp())
	}

	return Settings{
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
