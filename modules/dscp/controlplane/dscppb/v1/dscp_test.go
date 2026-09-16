package dscppb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	dscppb "github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

// Test_ShowConfigRequest_Validate verifies that an empty name is rejected.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &dscppb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &dscppb.ShowConfigRequest{Name: "dscp0"}},
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

// Test_AddPrefixesRequest_Validate verifies that an empty name is rejected.
func Test_AddPrefixesRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.AddPrefixesRequest
		message string
	}{
		{name: "empty name", request: &dscppb.AddPrefixesRequest{}, message: "name is required"},
		{name: "name set", request: &dscppb.AddPrefixesRequest{Name: "dscp0"}},
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

// Test_RemovePrefixesRequest_Validate verifies that an empty name is
// rejected.
func Test_RemovePrefixesRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.RemovePrefixesRequest
		message string
	}{
		{name: "empty name", request: &dscppb.RemovePrefixesRequest{}, message: "name is required"},
		{name: "name set", request: &dscppb.RemovePrefixesRequest{Name: "dscp0"}},
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

// Test_DeleteConfigRequest_Validate verifies that an empty name is rejected.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &dscppb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &dscppb.DeleteConfigRequest{Name: "dscp0"}},
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

// Test_SetDscpMarkingRequest_Validate verifies that a missing name, a
// missing dscp_config, and an out-of-range field are each rejected.
//
// The out-of-range case's message is delegated from the nested config.
func Test_SetDscpMarkingRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.SetDscpMarkingRequest
		message string
	}{
		{
			name:    "empty name",
			request: &dscppb.SetDscpMarkingRequest{DscpConfig: &dscppb.DscpConfig{}},
			message: "name is required",
		},
		{
			name:    "missing dscp_config",
			request: &dscppb.SetDscpMarkingRequest{Name: "dscp0"},
			message: "dscp_config is required",
		},
		{
			name: "dscp_config out of range",
			request: &dscppb.SetDscpMarkingRequest{
				Name:       "dscp0",
				DscpConfig: &dscppb.DscpConfig{Flag: 3},
			},
			message: "dscp_config: flag 3 must be in range 0..2",
		},
		{
			name: "valid",
			request: &dscppb.SetDscpMarkingRequest{
				Name:       "dscp0",
				DscpConfig: &dscppb.DscpConfig{Flag: 2, Mark: 8},
			},
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

// Test_DscpConfig_Validate verifies that flag and mark are each rejected
// once they exceed their valid range.
func Test_DscpConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		config  *dscppb.DscpConfig
		message string
	}{
		{name: "zero values", config: &dscppb.DscpConfig{}},
		{name: "flag at limit", config: &dscppb.DscpConfig{Flag: 2}},
		{name: "mark at limit", config: &dscppb.DscpConfig{Mark: 63}},
		{
			name:    "flag above limit",
			config:  &dscppb.DscpConfig{Flag: 3},
			message: "flag 3 must be in range 0..2",
		},
		{
			name:    "mark above limit",
			config:  &dscppb.DscpConfig{Mark: 64},
			message: "mark 64 must be in range 0..63",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
