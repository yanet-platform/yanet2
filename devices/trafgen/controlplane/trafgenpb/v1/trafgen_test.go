package trafgenpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	trafgenpb "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
)

// Test_UpdateDeviceRequest_Validate verifies that device names and nested
// device weights use the request field path in their errors.
func Test_UpdateDeviceRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *trafgenpb.UpdateDeviceRequest
		message string
	}{
		{
			name:    "empty name",
			request: &trafgenpb.UpdateDeviceRequest{},
			message: "name is required",
		},
		{
			name: "name contains NUL",
			request: &trafgenpb.UpdateDeviceRequest{
				Name: "edge\x00backup",
			},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &trafgenpb.UpdateDeviceRequest{
				Name:   strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
				Device: &commonpb.Device{},
			},
		},
		{
			name: "name reaches buffer limit",
			request: &trafgenpb.UpdateDeviceRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name: "invalid input weight",
			request: &trafgenpb.UpdateDeviceRequest{
				Name: "trafgen0",
				Device: &commonpb.Device{
					Input: []*commonpb.DevicePipeline{{Weight: 65536}},
				},
			},
			message: "device: input[0].weight 65536 must be in range 0..65535",
		},
		{
			name: "valid request",
			request: &trafgenpb.UpdateDeviceRequest{
				Name:   "trafgen0",
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

// Test_ShowConfigRequest_Validate verifies that every invalid device name is
// reported as an error on the name field.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *trafgenpb.ShowConfigRequest
		message string
	}{
		{
			name:    "empty name",
			request: &trafgenpb.ShowConfigRequest{},
			message: "name is required",
		},
		{
			name:    "name contains NUL",
			request: &trafgenpb.ShowConfigRequest{Name: "edge\x00backup"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &trafgenpb.ShowConfigRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &trafgenpb.ShowConfigRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "valid request",
			request: &trafgenpb.ShowConfigRequest{Name: "trafgen0"},
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

// Test_ShowPacketsRequest_Validate verifies that every invalid device name is
// reported as an error on the name field.
func Test_ShowPacketsRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *trafgenpb.ShowPacketsRequest
		message string
	}{
		{
			name:    "empty name",
			request: &trafgenpb.ShowPacketsRequest{},
			message: "name is required",
		},
		{
			name:    "name contains NUL",
			request: &trafgenpb.ShowPacketsRequest{Name: "edge\x00backup"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &trafgenpb.ShowPacketsRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &trafgenpb.ShowPacketsRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "valid request",
			request: &trafgenpb.ShowPacketsRequest{Name: "trafgen0"},
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

// Test_UploadPcapRequest_Validate verifies that every invalid device name is
// reported as an error on the name field.
func Test_UploadPcapRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *trafgenpb.UploadPcapRequest
		message string
	}{
		{
			name:    "empty name",
			request: &trafgenpb.UploadPcapRequest{},
			message: "name is required",
		},
		{
			name:    "name contains NUL",
			request: &trafgenpb.UploadPcapRequest{Name: "edge\x00backup"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &trafgenpb.UploadPcapRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &trafgenpb.UploadPcapRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "valid request",
			request: &trafgenpb.UploadPcapRequest{Name: "trafgen0"},
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

// Test_SetRateRequest_Validate verifies that every invalid device name is
// reported as an error on the name field.
func Test_SetRateRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *trafgenpb.SetRateRequest
		message string
	}{
		{
			name:    "empty name",
			request: &trafgenpb.SetRateRequest{},
			message: "name is required",
		},
		{
			name:    "name contains NUL",
			request: &trafgenpb.SetRateRequest{Name: "edge\x00backup"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &trafgenpb.SetRateRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen-1),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &trafgenpb.SetRateRequest{
				Name: strings.Repeat("a", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "valid request",
			request: &trafgenpb.SetRateRequest{Name: "trafgen0"},
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
