package vxlan

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// DeviceVxlanService implements the DeviceVxlan gRPC service.
type DeviceVxlanService struct {
	vxlanpb.UnimplementedDeviceVxlanServiceServer

	mu      sync.Mutex
	agent   *ffi.Agent
	configs map[string]*cachedDevice
}

// cachedDevice is the last applied configuration of one device: the
// shared-memory device freed when a later update supersedes it, and the
// request fields served back by GetDevice.
type cachedDevice struct {
	config   DeviceConfig
	device   *commonpb.Device
	settings Settings
}

// Free releases the shared-memory device the cached entry owns.
func (m *cachedDevice) Free() {
	m.config.Free()
}

// GetResponse renders the cached entry as a GetDevice reply.
//
// Must be called with the service mutex held: UpdateDevice replaces the
// entry and frees the old device under it, which would race an unlocked
// reader of the cached fields.
func (m *cachedDevice) GetResponse(name string) *vxlanpb.GetDeviceVxlanResponse {
	return &vxlanpb.GetDeviceVxlanResponse{
		Name:    name,
		Device:  m.device,
		Vni:     m.settings.VNI,
		DstPort: uint32(m.settings.DstPort),
		SrcMac:  net.HardwareAddr(m.settings.SrcMAC[:]).String(),
		DstMac:  net.HardwareAddr(m.settings.DstMAC[:]).String(),
		SrcIp:   m.settings.SrcIP.String(),
		DstIp:   m.settings.DstIP.String(),
	}
}

// NewDeviceVxlanService creates a service publishing devices through the
// given agent.
func NewDeviceVxlanService(agent *ffi.Agent) *DeviceVxlanService {
	return &DeviceVxlanService{
		agent:   agent,
		configs: map[string]*cachedDevice{},
	}
}

// UpdateDevice installs the pipelines and tunnel parameters of one vxlan
// device.
func (m *DeviceVxlanService) UpdateDevice(
	ctx context.Context,
	request *vxlanpb.UpdateDeviceVxlanRequest,
) (*vxlanpb.UpdateDeviceVxlanResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	settings, err := newSettings(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	deviceConfig, err := NewDeviceConfig(m.agent, name, request.GetDevice(), settings)
	if err != nil {
		return nil, fmt.Errorf("failed to create device config: %w", err)
	}

	if err := m.agent.UpdateDevices([]ffi.ShmDeviceConfig{deviceConfig.AsFFIDevice()}); err != nil {
		deviceConfig.Free()
		return nil, fmt.Errorf("failed to update device: %w", err)
	}

	// UpdateDevices publishes the new generation and waits for the
	// dataplane to drop the old one, so the superseded device is no longer
	// referenced and can be freed explicitly. This mirrors how the vlan
	// control plane reclaims superseded device configs, instead of relying
	// on a type-blind drain of the agent's unused list.
	if old, ok := m.configs[name]; ok {
		old.Free()
	}
	m.configs[name] = &cachedDevice{
		config:   *deviceConfig,
		device:   copyDevice(request.GetDevice()),
		settings: settings,
	}

	return &vxlanpb.UpdateDeviceVxlanResponse{}, nil
}

// GetDevice returns the last applied configuration of the named device.
func (m *DeviceVxlanService) GetDevice(
	ctx context.Context,
	request *vxlanpb.GetDeviceVxlanRequest,
) (*vxlanpb.GetDeviceVxlanResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cached, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	return cached.GetResponse(name), nil
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
