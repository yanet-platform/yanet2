package vxlanpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	vxlanpb "github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// newTunnelRequest returns a request carrying the smallest configuration
// every validation accepts.
func newTunnelRequest(name string) *vxlanpb.UpdateDeviceVxlanRequest {
	return &vxlanpb.UpdateDeviceVxlanRequest{
		Name:    name,
		Device:  &commonpb.Device{},
		Vni:     4242,
		DstPort: 4789,
		SrcMac:  "02:00:00:00:00:01",
		DstMac:  "02:00:00:00:00:02",
		SrcIp:   "10.0.0.1",
		DstIp:   "10.0.0.2",
	}
}

// Test_UpdateDeviceVxlanRequest_Validate verifies that device names, nested
// device weights, and the tunnel scalars report their request field paths.
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
				Name: "tun0\x00backup",
			},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: func() *vxlanpb.UpdateDeviceVxlanRequest {
				request := newTunnelRequest(strings.Repeat("a", commonpb.MaxDeviceNameLen-1))
				return request
			}(),
		},
		{
			name: "name reaches buffer limit",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name: "invalid input weight",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name: "tun0",
				Device: &commonpb.Device{
					Input: []*commonpb.DevicePipeline{{Weight: 65536}},
				},
			},
			message: "device: input[0].weight 65536 must be in range 0..65535",
		},
		{
			name: "VNI above maximum",
			request: func() *vxlanpb.UpdateDeviceVxlanRequest {
				request := newTunnelRequest("tun0")
				request.Vni = 1 << 24
				return request
			}(),
			message: "vni 16777216 must be in range 0..16777215",
		},
		{
			name: "VNI at maximum",
			request: func() *vxlanpb.UpdateDeviceVxlanRequest {
				request := newTunnelRequest("tun0")
				request.Vni = 0xFFFFFF
				return request
			}(),
		},
		{
			name: "zero destination port",
			request: &vxlanpb.UpdateDeviceVxlanRequest{
				Name: "tun0",
			},
			message: "dst_port 0 must be in range 1..65535",
		},
		{
			name: "destination port above maximum",
			request: func() *vxlanpb.UpdateDeviceVxlanRequest {
				request := newTunnelRequest("tun0")
				request.DstPort = 65536
				return request
			}(),
			message: "dst_port 65536 must be in range 1..65535",
		},
		{
			name: "destination port at maximum",
			request: func() *vxlanpb.UpdateDeviceVxlanRequest {
				request := newTunnelRequest("tun0")
				request.DstPort = 65535
				return request
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_GetDeviceVxlanRequest_Validate verifies that a read without a
// usable device name is rejected before the service is consulted.
func Test_GetDeviceVxlanRequest_Validate(t *testing.T) {
	require.EqualError(
		t,
		(&vxlanpb.GetDeviceVxlanRequest{}).Validate(),
		"name is required",
	)
	require.NoError(
		t,
		(&vxlanpb.GetDeviceVxlanRequest{Name: "tun0"}).Validate(),
	)
}
