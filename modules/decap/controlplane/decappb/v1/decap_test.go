package decappb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// Test_ShowConfigRequest_Validate verifies that an empty name is rejected
// and that a nil receiver reports the same error without panicking.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *decappb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &decappb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &decappb.ShowConfigRequest{Name: "decap0"}},
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
		request *decappb.UpdateConfigRequest
		message string
	}{
		{name: "empty name", request: &decappb.UpdateConfigRequest{}, message: "name is required"},
		{name: "name set", request: &decappb.UpdateConfigRequest{Name: "decap0"}},
		{
			name: "name set with an invalid prefix",
			request: &decappb.UpdateConfigRequest{
				Name:      "decap0",
				Prefixes4: []*commonpb.IPv4Prefix{{PrefixLen: 24}},
			},
		},
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
		request *decappb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &decappb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &decappb.DeleteConfigRequest{Name: "decap0"}},
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
