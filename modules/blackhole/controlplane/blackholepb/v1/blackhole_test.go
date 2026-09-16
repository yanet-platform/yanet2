package blackholepb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

// Test_ShowConfigRequest_Validate verifies that an empty name is rejected
// and that a nil receiver reports the same error without panicking.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *blackholepb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &blackholepb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &blackholepb.ShowConfigRequest{Name: "blackhole0"}},
		{name: "nil request", request: nil, message: "name is required"},
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

// Test_UpdateConfigRequest_Validate verifies that an empty name is rejected
// and that a nil receiver reports the same error without panicking.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *blackholepb.UpdateConfigRequest
		message string
	}{
		{name: "empty name", request: &blackholepb.UpdateConfigRequest{}, message: "name is required"},
		{name: "name set", request: &blackholepb.UpdateConfigRequest{Name: "blackhole0"}},
		{name: "nil request", request: nil, message: "name is required"},
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

// Test_DeleteConfigRequest_Validate verifies that an empty name is rejected
// and that a nil receiver reports the same error without panicking.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *blackholepb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &blackholepb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &blackholepb.DeleteConfigRequest{Name: "blackhole0"}},
		{name: "nil request", request: nil, message: "name is required"},
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
