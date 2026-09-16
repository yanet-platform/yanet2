package aclpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

// Test_ShowConfigRequest_Validate verifies that an empty configuration name
// is rejected with the request's field-level error.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *aclpb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &aclpb.ShowConfigRequest{}, message: "name is required"},
		{name: "named request", request: &aclpb.ShowConfigRequest{Name: "acl0"}},
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

// Test_UpdateConfigRequest_Validate verifies that update-only request rules
// report their own field-level errors.
func Test_UpdateConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *aclpb.UpdateConfigRequest
		message string
	}{
		{name: "empty name", request: &aclpb.UpdateConfigRequest{}, message: "name is required"},
		{
			name:    "nil request",
			request: nil,
			message: "name is required",
		},
		{
			name: "nil rules",
			request: &aclpb.UpdateConfigRequest{
				Name: "acl0",
			},
			message: "rules must contain at least one rule",
		},
		{
			name: "empty rules",
			request: &aclpb.UpdateConfigRequest{
				Name:  "acl0",
				Rules: []*aclpb.Rule{},
			},
			message: "rules must contain at least one rule",
		},
		{
			name: "deprecated sync config",
			request: &aclpb.UpdateConfigRequest{
				Name:       "acl0",
				Rules:      []*aclpb.Rule{{}},
				SyncConfig: &aclpb.SyncConfig{},
			},
			message: "sync_config belongs to fwstate",
		},
		{
			name: "v4 map name contains NUL",
			request: &aclpb.UpdateConfigRequest{
				Name:          "acl0",
				Rules:         []*aclpb.Rule{{}},
				FwtableNameV4: "map\x00name",
			},
			message: "fwtable_name_v4 must not contain NUL",
		},
		{
			name: "v4 map name reaches byte limit",
			request: &aclpb.UpdateConfigRequest{
				Name:          "acl0",
				Rules:         []*aclpb.Rule{{}},
				FwtableNameV4: strings.Repeat("a", fwstatemappb.MaxMapNameLen),
			},
			message: "fwtable_name_v4 must be shorter than 80 bytes",
		},
		{
			name: "v6 map name contains NUL",
			request: &aclpb.UpdateConfigRequest{
				Name:          "acl0",
				Rules:         []*aclpb.Rule{{}},
				FwtableNameV6: "map\x00name",
			},
			message: "fwtable_name_v6 must not contain NUL",
		},
		{
			name: "v6 map name reaches byte limit",
			request: &aclpb.UpdateConfigRequest{
				Name:          "acl0",
				Rules:         []*aclpb.Rule{{}},
				FwtableNameV6: strings.Repeat("a", fwstatemappb.MaxMapNameLen),
			},
			message: "fwtable_name_v6 must be shorter than 80 bytes",
		},
		{
			name: "valid request at map name boundary",
			request: &aclpb.UpdateConfigRequest{
				Name:          "acl0",
				Rules:         []*aclpb.Rule{{}},
				FwtableNameV4: strings.Repeat("a", fwstatemappb.MaxMapNameLen-1),
				FwtableNameV6: strings.Repeat("b", fwstatemappb.MaxMapNameLen-1),
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

// Test_DeleteConfigRequest_Validate verifies that an empty configuration name
// is rejected with the request's field-level error.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *aclpb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &aclpb.DeleteConfigRequest{}, message: "name is required"},
		{name: "named request", request: &aclpb.DeleteConfigRequest{Name: "acl0"}},
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

// Test_GetMetricsRulesRequest_Validate verifies that each selector rejects
// values that cannot fit in its fixed counter-tag value field.
func Test_GetMetricsRulesRequest_Validate(t *testing.T) {
	overlong := strings.Repeat("a", ynpb.MaxCounterTagValueLen)
	cases := []struct {
		name    string
		request *aclpb.GetMetricsRulesRequest
		message string
	}{
		{name: "nil request", request: nil},
		{name: "empty request", request: &aclpb.GetMetricsRulesRequest{}},
		{
			name:    "config contains NUL",
			request: &aclpb.GetMetricsRulesRequest{Config: "acl\x00"},
			message: "config must not contain NUL",
		},
		{
			name:    "device contains NUL",
			request: &aclpb.GetMetricsRulesRequest{Device: "port\x00"},
			message: "device must not contain NUL",
		},
		{
			name:    "pipeline contains NUL",
			request: &aclpb.GetMetricsRulesRequest{Pipeline: "pipe\x00"},
			message: "pipeline must not contain NUL",
		},
		{
			name:    "function contains NUL",
			request: &aclpb.GetMetricsRulesRequest{Function: "function\x00"},
			message: "function must not contain NUL",
		},
		{
			name:    "chain contains NUL",
			request: &aclpb.GetMetricsRulesRequest{Chain: "chain\x00"},
			message: "chain must not contain NUL",
		},
		{
			name:    "config reaches byte limit",
			request: &aclpb.GetMetricsRulesRequest{Config: overlong},
			message: "config must be shorter than 80 bytes",
		},
		{
			name:    "device reaches byte limit",
			request: &aclpb.GetMetricsRulesRequest{Device: overlong},
			message: "device must be shorter than 80 bytes",
		},
		{
			name:    "pipeline reaches byte limit",
			request: &aclpb.GetMetricsRulesRequest{Pipeline: overlong},
			message: "pipeline must be shorter than 80 bytes",
		},
		{
			name:    "function reaches byte limit",
			request: &aclpb.GetMetricsRulesRequest{Function: overlong},
			message: "function must be shorter than 80 bytes",
		},
		{
			name:    "chain reaches byte limit",
			request: &aclpb.GetMetricsRulesRequest{Chain: overlong},
			message: "chain must be shorter than 80 bytes",
		},
		{
			name: "wildcard selectors",
			request: &aclpb.GetMetricsRulesRequest{
				Config:   "*",
				Device:   "*",
				Pipeline: "*",
				Function: "*",
				Chain:    "*",
			},
		},
		{
			name: "selectors at accepted byte boundary",
			request: &aclpb.GetMetricsRulesRequest{
				Config: strings.Repeat(
					"a",
					ynpb.MaxCounterTagValueLen-1,
				),
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
