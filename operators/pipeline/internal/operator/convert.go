package operator

import (
	"fmt"
	"net"
	"net/netip"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
	"github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

func pipelineToProto(p PipelineConfig) *ynpb.Pipeline {
	functions := make([]*commonpb.FunctionId, len(p.Functions))
	for idx, name := range p.Functions {
		functions[idx] = &commonpb.FunctionId{Name: name}
	}

	return &ynpb.Pipeline{
		Id:        &commonpb.PipelineId{Name: p.Name},
		Functions: functions,
	}
}

func plainDeviceToProto(d DeviceConfig) *plainpb.UpdateDevicePlainRequest {
	return &plainpb.UpdateDevicePlainRequest{
		Name: d.Name,
		Device: &commonpb.Device{
			Input:  devicePipelineToProto(d.Input),
			Output: devicePipelineToProto(d.Output),
		},
	}
}

func vlanDeviceToProto(d VLANDeviceConfig) *vlanpb.UpdateDeviceVlanRequest {
	return &vlanpb.UpdateDeviceVlanRequest{
		Name: d.Name,
		Vlan: d.VLAN,
		Device: &commonpb.Device{
			Input:  devicePipelineToProto(d.Input),
			Output: devicePipelineToProto(d.Output),
		},
	}
}

// vxlanDeviceToProto builds the update request of a vxlan device, refusing a
// tunnel address that is not written in its text form.
func vxlanDeviceToProto(d VXLANDeviceConfig) (*vxlanpb.UpdateDeviceVxlanRequest, error) {
	tunnel, err := vxlanTunnelToProto(d.Tunnel)
	if err != nil {
		return nil, fmt.Errorf("tunnel: %w", err)
	}

	return &vxlanpb.UpdateDeviceVxlanRequest{
		Name:   d.Name,
		Tunnel: tunnel,
		Device: &commonpb.Device{
			Input:  devicePipelineToProto(d.Input),
			Output: devicePipelineToProto(d.Output),
		},
	}, nil
}

func vxlanTunnelToProto(t VXLANTunnelConfig) (*vxlanpb.VxlanTunnel, error) {
	localIP, err := parseIPv4(t.LocalIP)
	if err != nil {
		return nil, fmt.Errorf("local_ip: %w", err)
	}
	remoteIP, err := parseIPv4(t.RemoteIP)
	if err != nil {
		return nil, fmt.Errorf("remote_ip: %w", err)
	}
	localMAC, err := parseEUI48(t.LocalMAC)
	if err != nil {
		return nil, fmt.Errorf("local_mac: %w", err)
	}
	remoteMAC, err := parseEUI48(t.RemoteMAC)
	if err != nil {
		return nil, fmt.Errorf("remote_mac: %w", err)
	}

	return &vxlanpb.VxlanTunnel{
		LocalIp:   localIP,
		RemoteIp:  remoteIP,
		LocalMac:  localMAC,
		RemoteMac: remoteMAC,
		Vni:       t.VNI,
	}, nil
}

func parseIPv4(text string) (*commonpb.IPv4Address, error) {
	addr, err := netip.ParseAddr(text)
	if err != nil {
		return nil, err
	}

	return commonpb.NewIPv4AddressFromAddr(addr)
}

func parseEUI48(text string) (*commonpb.MACAddress, error) {
	mac, err := net.ParseMAC(text)
	if err != nil {
		return nil, err
	}
	if len(mac) != 6 {
		return nil, fmt.Errorf("%q is not an EUI-48 address", text)
	}

	return commonpb.NewMACAddressEUI48([6]byte(mac)), nil
}

func devicePipelineToProto(refs []PipelineRefConfig) []*commonpb.DevicePipeline {
	out := make([]*commonpb.DevicePipeline, len(refs))
	for idx, r := range refs {
		out[idx] = &commonpb.DevicePipeline{
			Name:   r.Name,
			Weight: r.Weight,
		}
	}

	return out
}

// devicePipelineRefStrings renders pipeline refs as "name(weight=N)"
// strings for human-readable log output.
func devicePipelineRefStrings(refs []PipelineRefConfig) []string {
	out := make([]string, len(refs))
	for idx, r := range refs {
		out[idx] = fmt.Sprintf("%s(weight=%d)", r.Name, r.Weight)
	}

	return out
}
