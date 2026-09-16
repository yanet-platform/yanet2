package operatorpb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// Test_ShowRoutesRequest_Validate verifies that route listing requires a
// config name while leaving the route filters to the handler.
func Test_ShowRoutesRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *operatorpb.ShowRoutesRequest
		message string
	}{
		{name: "empty name", request: &operatorpb.ShowRoutesRequest{}, message: "name is required"},
		{name: "name set", request: &operatorpb.ShowRoutesRequest{Name: "route0"}},
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

// Test_LookupRouteRequest_Validate verifies that lookup requires a config
// name while leaving address decoding to the handler.
func Test_LookupRouteRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *operatorpb.LookupRouteRequest
		message string
	}{
		{name: "empty name", request: &operatorpb.LookupRouteRequest{}, message: "name is required"},
		{name: "name set without address", request: &operatorpb.LookupRouteRequest{Name: "route0"}},
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

// Test_InsertRouteRequest_Validate verifies that route insertion requires
// nexthops and limits non-static routes to one nexthop.
func Test_InsertRouteRequest_Validate(t *testing.T) {
	nexthops := []*commonpb.IPAddress{{}, {}}

	cases := []struct {
		name    string
		request *operatorpb.InsertRouteRequest
		message string
	}{
		{name: "empty name", request: &operatorpb.InsertRouteRequest{}, message: "name is required"},
		{
			name:    "empty nexthops",
			request: &operatorpb.InsertRouteRequest{Name: "route0"},
			message: "nexthop_addrs is required",
		},
		{
			name: "multiple bird nexthops",
			request: &operatorpb.InsertRouteRequest{
				Name:         "route0",
				NexthopAddrs: nexthops,
				SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
			},
			message: "nexthop_addrs must contain at most one address for non-static source",
		},
		{
			name: "single bird nexthop",
			request: &operatorpb.InsertRouteRequest{
				Name:         "route0",
				NexthopAddrs: []*commonpb.IPAddress{{}},
				SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
			},
		},
		{
			name: "multiple static nexthops",
			request: &operatorpb.InsertRouteRequest{
				Name:         "route0",
				NexthopAddrs: nexthops,
				SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
			},
		},
		{
			name: "unset source defaults to static",
			request: &operatorpb.InsertRouteRequest{
				Name:         "route0",
				NexthopAddrs: nexthops,
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

// Test_DeleteRouteRequest_Validate verifies that route deletion requires a
// name and at least one nexthop while leaving address and prefix parsing out.
func Test_DeleteRouteRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *operatorpb.DeleteRouteRequest
		message string
	}{
		{name: "empty name", request: &operatorpb.DeleteRouteRequest{}, message: "name is required"},
		{
			name:    "empty nexthops",
			request: &operatorpb.DeleteRouteRequest{Name: "route0"},
			message: "nexthop_addrs is required",
		},
		{
			name: "name and nexthop set without parsable values",
			request: &operatorpb.DeleteRouteRequest{
				Name:         "route0",
				NexthopAddrs: []*commonpb.IPAddress{{}},
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

// Test_FlushRoutesRequest_Validate verifies that flushing requires a config
// name and has no parser-dependent validation.
func Test_FlushRoutesRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *operatorpb.FlushRoutesRequest
		message string
	}{
		{name: "empty name", request: &operatorpb.FlushRoutesRequest{}, message: "name is required"},
		{name: "name set", request: &operatorpb.FlushRoutesRequest{Name: "route0"}},
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

// Test_Update_Validate verifies that every streamed update carries its
// target name while a missing route remains a valid flush event.
func Test_Update_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *operatorpb.Update
		message string
	}{
		{name: "empty name", request: &operatorpb.Update{}, message: "name is required"},
		{name: "named flush update", request: &operatorpb.Update{Name: "route0"}},
		{name: "named route update", request: &operatorpb.Update{Name: "route0", Route: &operatorpb.Route{}}},
		{name: "nil update", request: nil, message: "name is required"},
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
