package plainpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	plainpb "github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
)

// Test_UpdateDevicePlainRequest_Validate verifies that device names and
// nested device weights use the request field path in their errors.
func Test_UpdateDevicePlainRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *plainpb.UpdateDevicePlainRequest
		message string
	}{
		{
			name:    "empty name",
			request: &plainpb.UpdateDevicePlainRequest{},
			message: "name is required",
		},
		{
			name: "name contains NUL",
			request: &plainpb.UpdateDevicePlainRequest{
				Name: "edge\x00backup",
			},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &plainpb.UpdateDevicePlainRequest{
				Name:   strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
				Device: &commonpb.Device{},
			},
		},
		{
			name: "name reaches buffer limit",
			request: &plainpb.UpdateDevicePlainRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name: "invalid input weight",
			request: &plainpb.UpdateDevicePlainRequest{
				Name: "plain0",
				Device: &commonpb.Device{
					Input: []*commonpb.DevicePipeline{{Weight: 65536}},
				},
			},
			message: "device: input[0].weight 65536 must be in range 0..65535",
		},
		{
			name: "valid request",
			request: &plainpb.UpdateDevicePlainRequest{
				Name:   "plain0",
				Device: &commonpb.Device{},
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

// Test_ShowDevicePlainRequest_Validate verifies that every invalid device
// name is reported as an error on the name field.
func Test_ShowDevicePlainRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *plainpb.ShowDevicePlainRequest
		message string
	}{
		{
			name:    "empty name",
			request: &plainpb.ShowDevicePlainRequest{},
			message: "name is required",
		},
		{
			name:    "name contains NUL",
			request: &plainpb.ShowDevicePlainRequest{Name: "edge\x00backup"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &plainpb.ShowDevicePlainRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &plainpb.ShowDevicePlainRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "valid request",
			request: &plainpb.ShowDevicePlainRequest{Name: "plain0"},
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
