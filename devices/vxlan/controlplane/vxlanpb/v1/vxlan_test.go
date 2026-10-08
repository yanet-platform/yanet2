package vxlanpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	vxlanpb "github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// validTunnel returns a tunnel every rule accepts.
func validTunnel() *vxlanpb.VxlanTunnel {
	return &vxlanpb.VxlanTunnel{
		LocalIp:   commonpb.NewIPv4Address([4]byte{192, 0, 2, 1}),
		RemoteIp:  commonpb.NewIPv4Address([4]byte{198, 51, 100, 7}),
		LocalMac:  commonpb.NewMACAddressEUI48([6]byte{0x02, 0, 0, 0, 0, 0x01}),
		RemoteMac: commonpb.NewMACAddressEUI48([6]byte{0x02, 0, 0, 0, 0, 0x02}),
		Vni:       0x1234,
	}
}

// Test_VxlanTunnel_Validate verifies that every endpoint, hardware address
// and VNI rule reports the field it rejects.
func Test_VxlanTunnel_Validate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(tunnel *vxlanpb.VxlanTunnel)
		message string
	}{
		{
			name:   "valid tunnel",
			mutate: func(*vxlanpb.VxlanTunnel) {},
		},
		{
			name:    "missing local IP",
			mutate:  func(tunnel *vxlanpb.VxlanTunnel) { tunnel.LocalIp = nil },
			message: "local_ip is required",
		},
		{
			name: "unspecified local IP",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalIp = commonpb.NewIPv4Address([4]byte{})
			},
			message: "local_ip 0.0.0.0 must be a unicast address",
		},
		{
			name: "multicast local IP",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalIp = commonpb.NewIPv4Address([4]byte{224, 0, 0, 1})
			},
			message: "local_ip 224.0.0.1 must be a unicast address",
		},
		{
			name: "last multicast local IP",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalIp = commonpb.NewIPv4Address([4]byte{239, 255, 255, 255})
			},
			message: "local_ip 239.255.255.255 must be a unicast address",
		},
		{
			name: "first address above multicast",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalIp = commonpb.NewIPv4Address([4]byte{240, 0, 0, 1})
			},
		},
		{
			name:    "missing remote IP",
			mutate:  func(tunnel *vxlanpb.VxlanTunnel) { tunnel.RemoteIp = nil },
			message: "remote_ip is required",
		},
		{
			name: "broadcast remote IP",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.RemoteIp = commonpb.NewIPv4Address([4]byte{255, 255, 255, 255})
			},
			message: "remote_ip 255.255.255.255 must be a unicast address",
		},
		{
			name: "unspecified remote IP",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.RemoteIp = commonpb.NewIPv4Address([4]byte{})
			},
			message: "remote_ip 0.0.0.0 must be a unicast address",
		},
		{
			name:    "missing local MAC",
			mutate:  func(tunnel *vxlanpb.VxlanTunnel) { tunnel.LocalMac = nil },
			message: "local_mac is required",
		},
		{
			name: "zero local MAC",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalMac = commonpb.NewMACAddressEUI48([6]byte{})
			},
			message: "local_mac 00:00:00:00:00:00 must be a nonzero unicast address",
		},
		{
			name: "multicast local MAC",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalMac = commonpb.NewMACAddressEUI48([6]byte{0x01, 0x00, 0x5e, 0, 0, 1})
			},
			message: "local_mac 01:00:5e:00:00:01 must be a nonzero unicast address",
		},
		{
			name: "local MAC with set upper bits",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.LocalMac = &commonpb.MACAddress{Addr: 1<<48 | 0x020000000001}
			},
			message: "local_mac upper 16 bits must be zero",
		},
		{
			name:    "missing remote MAC",
			mutate:  func(tunnel *vxlanpb.VxlanTunnel) { tunnel.RemoteMac = nil },
			message: "remote_mac is required",
		},
		{
			name: "broadcast remote MAC",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) {
				tunnel.RemoteMac = commonpb.NewMACAddressEUI48([6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
			},
			message: "remote_mac ff:ff:ff:ff:ff:ff must be a nonzero unicast address",
		},
		{
			name:   "VNI at maximum",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) { tunnel.Vni = 16777215 },
		},
		{
			name:    "VNI above maximum",
			mutate:  func(tunnel *vxlanpb.VxlanTunnel) { tunnel.Vni = 16777216 },
			message: "vni 16777216 must be in range 0..16777215",
		},
		{
			name:   "VNI zero",
			mutate: func(tunnel *vxlanpb.VxlanTunnel) { tunnel.Vni = 0 },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tunnel := validTunnel()
			tc.mutate(tunnel)

			before := proto.Clone(tunnel)
			err := tunnel.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
			require.True(t, proto.Equal(before, tunnel))
		})
	}
}

// Test_UpdateDeviceVxlanRequest_Validate verifies that device names, nested
// device weights and the required tunnel report their request field paths.
func Test_UpdateDeviceVxlanRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *vxlanpb.UpdateDeviceVxlanRequest
		message string
	}{
		{
			name:    "empty name",
			request: &vxlanpb.UpdateDeviceVxlanRequest{},
			message: "name is required",
		},
		{
			name: "name contains NUL",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name: "edge\x00backup",
			},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
				Device: &commonpb.Device{},
				Tunnel: validTunnel(),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   strings.Repeat("a", commonpb.MaxDeviceNameLen),
				Tunnel: validTunnel(),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name: "invalid input weight",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name: "vxlan0",
				Device: &commonpb.Device{
					Input: []*commonpb.DevicePipeline{{Weight: 65536}},
				},
				Tunnel: validTunnel(),
			},
			message: "device: input[0].weight 65536 must be in range 0..65535",
		},
		{
			name: "missing tunnel",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   "vxlan0",
				Device: &commonpb.Device{},
			},
			message: "tunnel is required",
		},
		{
			name: "invalid tunnel",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   "vxlan0",
				Device: &commonpb.Device{},
				Tunnel: &vxlanpb.VxlanTunnel{},
			},
			message: "tunnel: local_ip is required",
		},
		{
			name: "VNI above maximum",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   "vxlan0",
				Device: &commonpb.Device{},
				Tunnel: func() *vxlanpb.VxlanTunnel {
					tunnel := validTunnel()
					tunnel.Vni = 1 << 24
					return tunnel
				}(),
			},
			message: "tunnel: vni 16777216 must be in range 0..16777215",
		},
		{
			name: "valid request",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name:   "vxlan0",
				Device: &commonpb.Device{},
				Tunnel: validTunnel(),
			},
		},
		{
			name:    "nil request",
			request: nil,
			message: "name is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_ShowDeviceVxlanRequest_Validate verifies that every invalid device
// name is reported as an error on the name field.
func Test_ShowDeviceVxlanRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *vxlanpb.ShowDeviceVxlanRequest
		message string
	}{
		{
			name:    "empty name",
			request: &vxlanpb.ShowDeviceVxlanRequest{},
			message: "name is required",
		},
		{
			name:    "name contains NUL",
			request: &vxlanpb.ShowDeviceVxlanRequest{Name: "edge\x00backup"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &vxlanpb.ShowDeviceVxlanRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &vxlanpb.ShowDeviceVxlanRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "valid request",
			request: &vxlanpb.ShowDeviceVxlanRequest{Name: "vxlan0"},
		},
		{
			name:    "nil request",
			request: nil,
			message: "name is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
