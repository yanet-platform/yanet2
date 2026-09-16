package fwstatemappb_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

// Test_ValidateMapName verifies that names outside the C registry contract
// are rejected with exact plain-error text.
func Test_ValidateMapName(t *testing.T) {
	cases := []struct {
		name    string
		mapName string
		message string
	}{
		{name: "empty name", mapName: "", message: "map name is required"},
		{name: "ordinary name", mapName: "fwstate0-v4"},
		{
			name:    "longest accepted name",
			mapName: strings.Repeat("a", fwstatemappb.MaxMapNameLen-1),
		},
		{
			name:    "name at byte limit",
			mapName: strings.Repeat("a", fwstatemappb.MaxMapNameLen),
			message: "map name must be shorter than 80 bytes",
		},
		{
			name:    "name beyond byte limit",
			mapName: strings.Repeat("a", fwstatemappb.MaxMapNameLen+120),
			message: "map name must be shorter than 80 bytes",
		},
		{
			name:    "name contains NUL",
			mapName: "a\x00b",
			message: "map name must not contain NUL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fwstatemappb.ValidateMapName(tc.mapName)
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_ValidateMapNameField verifies that field-aware validation reports the
// caller's proto field while preserving the shared map-name rules.
func Test_ValidateMapNameField(t *testing.T) {
	cases := []struct {
		name    string
		field   string
		mapName string
		message string
	}{
		{
			name:    "required field",
			field:   "map_name_v4",
			message: "map_name_v4 is required",
		},
		{
			name:    "embedded NUL field",
			field:   "fwtable_name_v4",
			mapName: "map\x00name",
			message: "fwtable_name_v4 must not contain NUL",
		},
		{
			name:    "field at byte limit",
			field:   "map_name_v4",
			mapName: strings.Repeat("a", fwstatemappb.MaxMapNameLen),
			message: "map_name_v4 must be shorter than 80 bytes",
		},
		{
			name:    "longest accepted field",
			field:   "fwtable_name_v4",
			mapName: strings.Repeat("a", fwstatemappb.MaxMapNameLen-1),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fwstatemappb.ValidateMapNameField(tc.field, tc.mapName)
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_CreateMapRequest_Validate verifies that the map name and address-family
// enum are validated before a create request can reach stateful work.
func Test_CreateMapRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *fwstatemappb.CreateMapRequest
		message string
	}{
		{
			name:    "empty name",
			request: &fwstatemappb.CreateMapRequest{},
			message: "name is required",
		},
		{
			name: "name at byte limit",
			request: &fwstatemappb.CreateMapRequest{
				Name: strings.Repeat("a", 80),
			},
			message: "name must be shorter than 80 bytes",
		},
		{
			name: "name contains NUL",
			request: &fwstatemappb.CreateMapRequest{
				Name: "map\x00name",
			},
			message: "name must not contain NUL",
		},
		{
			name: "unknown kind",
			request: &fwstatemappb.CreateMapRequest{
				Name: "map",
				Kind: fwstatemappb.Kind(2),
			},
			message: "kind unknown value 2",
		},
		{
			name:    "valid IPv4 request",
			request: &fwstatemappb.CreateMapRequest{Name: "map", Kind: fwstatemappb.Kind_V4},
		},
		{
			name:    "valid IPv6 request",
			request: &fwstatemappb.CreateMapRequest{Name: "map", Kind: fwstatemappb.Kind_V6},
		},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_DeleteMapRequest_Validate verifies that every map-name representation
// rule applies to deletion requests, including nil receivers.
func Test_DeleteMapRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *fwstatemappb.DeleteMapRequest
		message string
	}{
		{name: "empty name", request: &fwstatemappb.DeleteMapRequest{}, message: "name is required"},
		{
			name:    "name contains NUL",
			request: &fwstatemappb.DeleteMapRequest{Name: "map\x00name"},
			message: "name must not contain NUL",
		},
		{
			name:    "name at byte limit",
			request: &fwstatemappb.DeleteMapRequest{Name: strings.Repeat("a", 80)},
			message: "name must be shorter than 80 bytes",
		},
		{name: "valid name", request: &fwstatemappb.DeleteMapRequest{Name: "map"}},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_GetMapStatsRequest_Validate verifies that statistics requests use the
// same complete map-name rule as creation and deletion.
func Test_GetMapStatsRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *fwstatemappb.GetMapStatsRequest
		message string
	}{
		{name: "empty name", request: &fwstatemappb.GetMapStatsRequest{}, message: "name is required"},
		{
			name:    "name contains NUL",
			request: &fwstatemappb.GetMapStatsRequest{Name: "map\x00name"},
			message: "name must not contain NUL",
		},
		{
			name:    "name at byte limit",
			request: &fwstatemappb.GetMapStatsRequest{Name: strings.Repeat("a", 80)},
			message: "name must be shorter than 80 bytes",
		},
		{name: "valid name", request: &fwstatemappb.GetMapStatsRequest{Name: "map"}},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_InsertLayerRequest_Validate verifies that layer insertion applies the
// complete map-name rule before worker-count resolution.
func Test_InsertLayerRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *fwstatemappb.InsertLayerRequest
		message string
	}{
		{name: "empty name", request: &fwstatemappb.InsertLayerRequest{}, message: "name is required"},
		{
			name:    "name contains NUL",
			request: &fwstatemappb.InsertLayerRequest{Name: "map\x00name"},
			message: "name must not contain NUL",
		},
		{
			name:    "name at byte limit",
			request: &fwstatemappb.InsertLayerRequest{Name: strings.Repeat("a", 80)},
			message: "name must be shorter than 80 bytes",
		},
		{name: "valid name", request: &fwstatemappb.InsertLayerRequest{Name: "map"}},
		{name: "nil request", request: nil, message: "name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Test_ListEntriesRequest_Validate verifies that map-name, direction, and
// direction-specific cursor rules are reported at their request fields.
func Test_ListEntriesRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *fwstatemappb.ListEntriesRequest
		message string
	}{
		{
			name:    "empty map name",
			request: &fwstatemappb.ListEntriesRequest{Direction: fwstatemappb.Direction_FORWARD},
			message: "map_name is required",
		},
		{
			name: "map name contains NUL",
			request: &fwstatemappb.ListEntriesRequest{
				MapName: "map\x00name",
			},
			message: "map_name must not contain NUL",
		},
		{
			name: "map name at byte limit",
			request: &fwstatemappb.ListEntriesRequest{
				MapName: strings.Repeat("a", 80),
			},
			message: "map_name must be shorter than 80 bytes",
		},
		{
			name: "unknown direction",
			request: &fwstatemappb.ListEntriesRequest{
				MapName:   "map",
				Direction: fwstatemappb.Direction(2),
			},
			message: "direction unknown value 2",
		},
		{
			name: "negative forward index",
			request: &fwstatemappb.ListEntriesRequest{
				MapName:   "map",
				Direction: fwstatemappb.Direction_FORWARD,
				Index:     -1,
			},
			message: "index must not be negative for forward reads",
		},
		{
			name: "minimum forward index",
			request: &fwstatemappb.ListEntriesRequest{
				MapName:   "map",
				Direction: fwstatemappb.Direction_FORWARD,
				Index:     math.MinInt64,
			},
			message: "index must not be negative for forward reads",
		},
		{
			name: "zero forward index",
			request: &fwstatemappb.ListEntriesRequest{
				MapName:   "map",
				Direction: fwstatemappb.Direction_FORWARD,
			},
		},
		{
			name: "negative backward sentinel",
			request: &fwstatemappb.ListEntriesRequest{
				MapName:   "map",
				Direction: fwstatemappb.Direction_BACKWARD,
				Index:     -1,
			},
		},
		{
			name: "zero backward index",
			request: &fwstatemappb.ListEntriesRequest{
				MapName:   "map",
				Direction: fwstatemappb.Direction_BACKWARD,
			},
		},
		{name: "nil request", request: nil, message: "map_name is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}
