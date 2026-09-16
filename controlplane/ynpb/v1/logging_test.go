package ynpb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// Test_UpdateLevelRequest_Validate verifies that only logging levels exposed
// by the API are accepted and unknown enum numbers name the level field.
func Test_UpdateLevelRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *ynpb.UpdateLevelRequest
		message string
	}{
		{name: "info", request: &ynpb.UpdateLevelRequest{Level: ynpb.LogLevel_INFO}},
		{name: "debug", request: &ynpb.UpdateLevelRequest{Level: ynpb.LogLevel_DEBUG}},
		{name: "warn", request: &ynpb.UpdateLevelRequest{Level: ynpb.LogLevel_WARN}},
		{name: "error", request: &ynpb.UpdateLevelRequest{Level: ynpb.LogLevel_ERROR}},
		{
			name:    "unknown enum value",
			request: &ynpb.UpdateLevelRequest{Level: ynpb.LogLevel(99)},
			message: "level unknown value 99",
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
