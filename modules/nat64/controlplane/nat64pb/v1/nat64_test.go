package nat64pb_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	nat64pb "github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb/v1"
)

// Test_ShowConfigRequest_Validate verifies that an empty or nil request is
// rejected while a named request passes.
func Test_ShowConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *nat64pb.ShowConfigRequest
		message string
	}{
		{name: "empty name", request: &nat64pb.ShowConfigRequest{}, message: "name is required"},
		{name: "name set", request: &nat64pb.ShowConfigRequest{Name: "nat64-0"}},
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

// Test_AddPrefixRequest_Validate verifies that the name is checked before the
// required prefix and that prefix parsing remains outside request validation.
func Test_AddPrefixRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *nat64pb.AddPrefixRequest
		message string
	}{
		{
			name:    "empty name precedes missing prefix",
			request: &nat64pb.AddPrefixRequest{},
			message: "name is required",
		},
		{
			name:    "missing prefix",
			request: &nat64pb.AddPrefixRequest{Name: "nat64-0"},
			message: "prefix is required",
		},
		{
			name: "non-nil prefix",
			request: &nat64pb.AddPrefixRequest{
				Name:   "nat64-0",
				Prefix: &commonpb.IPv6Prefix{},
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

// Test_RemovePrefixRequest_Validate verifies that the name is checked before
// the required prefix and that prefix parsing remains outside validation.
func Test_RemovePrefixRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *nat64pb.RemovePrefixRequest
		message string
	}{
		{
			name:    "empty name precedes missing prefix",
			request: &nat64pb.RemovePrefixRequest{},
			message: "name is required",
		},
		{
			name:    "missing prefix",
			request: &nat64pb.RemovePrefixRequest{Name: "nat64-0"},
			message: "prefix is required",
		},
		{
			name: "non-nil prefix",
			request: &nat64pb.RemovePrefixRequest{
				Name:   "nat64-0",
				Prefix: &commonpb.IPv6Prefix{},
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

// Test_AddMappingRequest_Validate verifies that names and required address
// messages are checked in field order.
func Test_AddMappingRequest_Validate(t *testing.T) {
	ipv4 := commonpb.NewIPv4Address([4]byte{})
	ipv6 := commonpb.NewIPv6Address([16]byte{})

	cases := []struct {
		name    string
		request *nat64pb.AddMappingRequest
		message string
	}{
		{
			name:    "empty name precedes missing addresses",
			request: &nat64pb.AddMappingRequest{},
			message: "name is required",
		},
		{
			name:    "missing IPv4 address",
			request: &nat64pb.AddMappingRequest{Name: "nat64-0", Ipv6: ipv6},
			message: "ipv4 is required",
		},
		{
			name:    "missing IPv6 address",
			request: &nat64pb.AddMappingRequest{Name: "nat64-0", Ipv4: ipv4},
			message: "ipv6 is required",
		},
		{
			name: "missing both addresses keeps IPv4 first",
			request: &nat64pb.AddMappingRequest{
				Name: "nat64-0",
			},
			message: "ipv4 is required",
		},
		{
			name: "valid mapping",
			request: &nat64pb.AddMappingRequest{
				Name: "nat64-0",
				Ipv4: ipv4,
				Ipv6: ipv6,
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

// Test_RemoveMappingRequest_Validate verifies that the name is checked before
// the required IPv4 address.
func Test_RemoveMappingRequest_Validate(t *testing.T) {
	ipv4 := commonpb.NewIPv4Address([4]byte{})

	cases := []struct {
		name    string
		request *nat64pb.RemoveMappingRequest
		message string
	}{
		{
			name:    "empty name precedes missing address",
			request: &nat64pb.RemoveMappingRequest{},
			message: "name is required",
		},
		{
			name:    "missing IPv4 address",
			request: &nat64pb.RemoveMappingRequest{Name: "nat64-0"},
			message: "ipv4 is required",
		},
		{
			name:    "valid request",
			request: &nat64pb.RemoveMappingRequest{Name: "nat64-0", Ipv4: ipv4},
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

// Test_SetMTURequest_Validate verifies that names, the required MTU message,
// and nested MTU bounds are reported at their protobuf field paths.
func Test_SetMTURequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *nat64pb.SetMTURequest
		message string
	}{
		{
			name:    "empty name precedes missing MTU",
			request: &nat64pb.SetMTURequest{},
			message: "name is required",
		},
		{
			name:    "missing MTU",
			request: &nat64pb.SetMTURequest{Name: "nat64-0"},
			message: "mtu is required",
		},
		{
			name: "IPv4 MTU above maximum",
			request: &nat64pb.SetMTURequest{
				Name: "nat64-0",
				Mtu:  &nat64pb.MTUConfig{Ipv4Mtu: math.MaxUint16 + 1},
			},
			message: "mtu: ipv4_mtu 65536 must be in range 0..65535",
		},
		{
			name: "IPv6 MTU above maximum",
			request: &nat64pb.SetMTURequest{
				Name: "nat64-0",
				Mtu:  &nat64pb.MTUConfig{Ipv6Mtu: math.MaxUint16 + 1},
			},
			message: "mtu: ipv6_mtu 65536 must be in range 0..65535",
		},
		{
			name: "IPv4 MTU is checked before IPv6 MTU",
			request: &nat64pb.SetMTURequest{
				Name: "nat64-0",
				Mtu: &nat64pb.MTUConfig{
					Ipv4Mtu: math.MaxUint16 + 1,
					Ipv6Mtu: math.MaxUint16 + 1,
				},
			},
			message: "mtu: ipv4_mtu 65536 must be in range 0..65535",
		},
		{
			name: "explicit zero MTU is accepted",
			request: &nat64pb.SetMTURequest{
				Name: "nat64-0",
				Mtu:  &nat64pb.MTUConfig{},
			},
		},
		{
			name: "MTU values at maximum are accepted",
			request: &nat64pb.SetMTURequest{
				Name: "nat64-0",
				Mtu: &nat64pb.MTUConfig{
					Ipv4Mtu: math.MaxUint16,
					Ipv6Mtu: math.MaxUint16,
				},
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

// Test_MTUConfig_Validate verifies that both MTU fields accept zero and the
// maximum uint16 value while rejecting larger values.
func Test_MTUConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		config  *nat64pb.MTUConfig
		message string
	}{
		{name: "zero values", config: &nat64pb.MTUConfig{}},
		{
			name: "values at maximum",
			config: &nat64pb.MTUConfig{
				Ipv4Mtu: math.MaxUint16,
				Ipv6Mtu: math.MaxUint16,
			},
		},
		{
			name:    "IPv4 above maximum",
			config:  &nat64pb.MTUConfig{Ipv4Mtu: math.MaxUint16 + 1},
			message: "ipv4_mtu 65536 must be in range 0..65535",
		},
		{
			name:    "IPv6 above maximum",
			config:  &nat64pb.MTUConfig{Ipv6Mtu: math.MaxUint16 + 1},
			message: "ipv6_mtu 65536 must be in range 0..65535",
		},
		{
			name: "IPv4 is checked before IPv6",
			config: &nat64pb.MTUConfig{
				Ipv4Mtu: math.MaxUint16 + 1,
				Ipv6Mtu: math.MaxUint16 + 1,
			},
			message: "ipv4_mtu 65536 must be in range 0..65535",
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

// Test_SetDropUnknownRequest_Validate verifies that an empty or nil request
// is rejected while a named request passes.
func Test_SetDropUnknownRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *nat64pb.SetDropUnknownRequest
		message string
	}{
		{name: "empty name", request: &nat64pb.SetDropUnknownRequest{}, message: "name is required"},
		{name: "name set", request: &nat64pb.SetDropUnknownRequest{Name: "nat64-0"}},
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

// Test_DeleteConfigRequest_Validate verifies that an empty or nil request is
// rejected while a named request passes.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *nat64pb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &nat64pb.DeleteConfigRequest{}, message: "name is required"},
		{name: "name set", request: &nat64pb.DeleteConfigRequest{Name: "nat64-0"}},
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
