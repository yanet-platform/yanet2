package vlanpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	vlanpb "github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
)

// Test_UpdateDeviceVlanRequest_Validate verifies that device names, nested
// device weights, and VLAN ids report their request field paths.
func Test_UpdateDeviceVlanRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *vlanpb.UpdateDeviceVlanRequest
		message string
	}{
		{
			name:    "empty name",
			request: &vlanpb.UpdateDeviceVlanRequest{},
			message: "name is required",
		},
		{
			name: "name contains NUL",
			request: &vlanpb.UpdateDeviceVlanRequest{
				Name: "edge\x00backup",
			},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &vlanpb.UpdateDeviceVlanRequest{
				Name:   strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
				Device: &commonpb.Device{},
				Vlan:   4094,
			},
		},
		{
			name: "name reaches buffer limit",
			request: &vlanpb.UpdateDeviceVlanRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name: "invalid input weight",
			request: &vlanpb.UpdateDeviceVlanRequest{
				Name: "vlan0",
				Device: &commonpb.Device{
					Input: []*commonpb.DevicePipeline{{Weight: 65536}},
				},
			},
			message: "device: input[0].weight 65536 must be in range 0..65535",
		},
		{
			name: "VLAN above maximum",
			request: &vlanpb.UpdateDeviceVlanRequest{
				Name:   "vlan0",
				Device: &commonpb.Device{},
				Vlan:   4095,
			},
			message: "vlan 4095 must be in range 0..4094",
		},
		{
			name: "VLAN at maximum",
			request: &vlanpb.UpdateDeviceVlanRequest{
				Name:   "vlan0",
				Device: &commonpb.Device{},
				Vlan:   4094,
			},
		},
		{
			name: "valid request",
			request: &vlanpb.UpdateDeviceVlanRequest{
				Name:   "vlan0",
				Device: &commonpb.Device{},
				Vlan:   100,
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

// Test_ShowDeviceVlanRequest_Validate verifies that every invalid device
// name is reported as an error on the name field.
func Test_ShowDeviceVlanRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *vlanpb.ShowDeviceVlanRequest
		message string
	}{
		{
			name:    "empty name",
			request: &vlanpb.ShowDeviceVlanRequest{},
			message: "name is required",
		},
		{
			name:    "name contains NUL",
			request: &vlanpb.ShowDeviceVlanRequest{Name: "edge\x00backup"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &vlanpb.ShowDeviceVlanRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &vlanpb.ShowDeviceVlanRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "valid request",
			request: &vlanpb.ShowDeviceVlanRequest{Name: "vlan0"},
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
