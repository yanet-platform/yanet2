package ynpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_DeleteDeviceRequest_Validate verifies that a device name obeys the
// fixed-size buffer and NUL rules without using the FFI binding.
func Test_DeleteDeviceRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.DeleteDeviceRequest
		message string
	}{
		{name: "nil request", request: nil, message: "name is required"},
		{name: "empty name", request: &ynpb.DeleteDeviceRequest{}, message: "name is required"},
		{
			name:    "name contains NUL",
			request: &ynpb.DeleteDeviceRequest{Name: "edge\x00backup"},
			message: "name must not contain NUL",
		},
		{
			name: "name at usable limit",
			request: &ynpb.DeleteDeviceRequest{
				Name: strings.Repeat("d", commonpb.MaxDeviceNameLen-1),
			},
		},
		{
			name: "name reaches buffer limit",
			request: &ynpb.DeleteDeviceRequest{
				Name: strings.Repeat("d", commonpb.MaxDeviceNameLen),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name:    "named device",
			request: &ynpb.DeleteDeviceRequest{Name: "edge0"},
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
