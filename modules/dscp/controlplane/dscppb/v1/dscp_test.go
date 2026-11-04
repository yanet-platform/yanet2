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

// Test_UpdateConfigRequest_Validate verifies that the name and the config
// are required and that an out-of-range marking is reported at its field
// path.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *dscppb.UpdateConfigRequest
		message string
	}{
		{
			name:    "empty name precedes missing config",
			request: &dscppb.UpdateConfigRequest{},
			message: "name is required",
		},
		{
			name:    "missing config",
			request: &dscppb.UpdateConfigRequest{Name: "dscp0"},
			message: "config is required",
		},
		{
			name: "marking out of range",
			request: &dscppb.UpdateConfigRequest{
				Name:   "dscp0",
				Config: &dscppb.Config{DscpConfig: &dscppb.DscpConfig{Flag: 3}},
			},
			message: "config: dscp_config: flag 3 must be in range 0..2",
		},
		{
			name:    "config without a marking",
			request: &dscppb.UpdateConfigRequest{Name: "dscp0", Config: &dscppb.Config{}},
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

// Test_Config_Validate verifies that a set marking is range-checked at its
// field path while an unset one is accepted.
func Test_Config_Validate(t *testing.T) {
	cases := []struct {
		name    string
		config  *dscppb.Config
		message string
	}{
		{name: "no marking", config: &dscppb.Config{}},
		{
			name:   "marking at limit",
			config: &dscppb.Config{DscpConfig: &dscppb.DscpConfig{Flag: 2, Mark: 63}},
		},
		{
			name:    "mark above limit",
			config:  &dscppb.Config{DscpConfig: &dscppb.DscpConfig{Mark: 64}},
			message: "dscp_config: mark 64 must be in range 0..63",
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
