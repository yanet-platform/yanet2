package routepb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// validNexthop returns a complete nexthop with an empty optional counter.
func validNexthop() *routepb.FIBNexthop {
	return &routepb.FIBNexthop{
		DstMac: commonpb.NewMACAddressEUI48([6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}),
		SrcMac: commonpb.NewMACAddressEUI48([6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}),
		Device: "eth0",
	}
}

// Test_DeleteConfigRequest_Validate verifies that deletion requires a
// configuration name while a named request and nil receiver follow the
// expected validation contract.
func Test_DeleteConfigRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *routepb.DeleteConfigRequest
		message string
	}{
		{name: "empty name", request: &routepb.DeleteConfigRequest{}, message: "name is required"},
		{name: "named request", request: &routepb.DeleteConfigRequest{Name: "route0"}},
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

// Test_ShowFIBRequest_Validate verifies that FIB inspection requires a
// configuration name while a named request passes unchanged.
func Test_ShowFIBRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *routepb.ShowFIBRequest
		message string
	}{
		{name: "empty name", request: &routepb.ShowFIBRequest{}, message: "name is required"},
		{name: "named request", request: &routepb.ShowFIBRequest{Name: "route0"}},
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

// Test_UpdateFIBRequest_Validate verifies that module names and nested FIB
// entries are validated with their repeated field paths.
func Test_UpdateFIBRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *routepb.UpdateFIBRequest
		message string
	}{
		{
			name:    "empty module name",
			request: &routepb.UpdateFIBRequest{},
			message: "module_name is required",
		},
		{
			name: "invalid nested nexthop",
			request: &routepb.UpdateFIBRequest{
				ModuleName: "route0",
				Entries: []*routepb.FIBEntry{{
					Nexthops: []*routepb.FIBNexthop{{
						DstMac: commonpb.NewMACAddressEUI48([6]byte{}),
						Device: "eth0",
					}},
				}},
			},
			message: "entries[0]: nexthops[0]: src_mac is required",
		},
		{
			name: "valid request",
			request: &routepb.UpdateFIBRequest{
				ModuleName: "route0",
				Entries: []*routepb.FIBEntry{{Nexthops: []*routepb.FIBNexthop{
					validNexthop(),
				}}},
			},
		},
		{name: "nil request", request: nil, message: "module_name is required"},
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

// Test_FIBEntry_Validate verifies that nexthop errors retain their repeated
// field index while empty and valid entries pass.
func Test_FIBEntry_Validate(t *testing.T) {
	cases := []struct {
		name    string
		entry   *routepb.FIBEntry
		message string
	}{
		{name: "empty entry", entry: &routepb.FIBEntry{}},
		{
			name: "nil nexthop at repeated index",
			entry: &routepb.FIBEntry{
				Nexthops: []*routepb.FIBNexthop{validNexthop(), nil},
			},
			message: "nexthops[1]: src_mac is required",
		},
		{
			name: "valid entry",
			entry: &routepb.FIBEntry{
				Nexthops: []*routepb.FIBNexthop{validNexthop()},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.entry.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}

// Test_FIBNexthop_Validate verifies that required fields and C-bound device
// and explicit counter rules are reported with the field that failed.
func Test_FIBNexthop_Validate(t *testing.T) {
	counterPrefix := "nexthop_"

	cases := []struct {
		name    string
		nexthop *routepb.FIBNexthop
		message string
	}{
		{name: "nil nexthop", nexthop: nil, message: "src_mac is required"},
		{
			name: "missing source MAC",
			nexthop: &routepb.FIBNexthop{
				DstMac: commonpb.NewMACAddressEUI48([6]byte{}),
				Device: "eth0",
			},
			message: "src_mac is required",
		},
		{
			name: "missing destination MAC",
			nexthop: &routepb.FIBNexthop{
				SrcMac: commonpb.NewMACAddressEUI48([6]byte{}),
				Device: "eth0",
			},
			message: "dst_mac is required",
		},
		{
			name:    "missing device",
			nexthop: &routepb.FIBNexthop{SrcMac: commonpb.NewMACAddressEUI48([6]byte{}), DstMac: commonpb.NewMACAddressEUI48([6]byte{})},
			message: "device is required",
		},
		{
			name:    "device contains NUL",
			nexthop: func() *routepb.FIBNexthop { result := validNexthop(); result.Device = "eth\x00backup"; return result }(),
			message: "device must not contain NUL",
		},
		{
			name: "device reaches buffer limit",
			nexthop: func() *routepb.FIBNexthop {
				result := validNexthop()
				result.Device = strings.Repeat("d", commonpb.MaxDeviceNameLen)
				return result
			}(),
			message: "device must be shorter than 80 bytes",
		},
		{
			name: "device at usable limit",
			nexthop: func() *routepb.FIBNexthop {
				result := validNexthop()
				result.Device = strings.Repeat("d", commonpb.MaxDeviceNameLen-1)
				return result
			}(),
		},
		{name: "empty counter", nexthop: validNexthop()},
		{
			name: "counter misses prefix",
			nexthop: func() *routepb.FIBNexthop {
				result := validNexthop()
				result.Counter = "route_forwarded_v4"
				return result
			}(),
			message: `counter must start with "nexthop_"`,
		},
		{
			name: "counter contains NUL",
			nexthop: func() *routepb.FIBNexthop {
				result := validNexthop()
				result.Counter = counterPrefix + "ab\x00cd"
				return result
			}(),
			message: "counter must not contain NUL",
		},
		{
			name: "counter at usable limit",
			nexthop: func() *routepb.FIBNexthop {
				result := validNexthop()
				result.Counter = counterPrefix + strings.Repeat("c", routepb.MaxCounterNameLen-len(counterPrefix))
				return result
			}(),
		},
		{
			name: "counter reaches buffer limit",
			nexthop: func() *routepb.FIBNexthop {
				result := validNexthop()
				result.Counter = counterPrefix + strings.Repeat("c", routepb.MaxCounterNameLen+1-len(counterPrefix))
				return result
			}(),
			message: "counter must be shorter than 128 bytes",
		},
		{
			name: "valid explicit counter",
			nexthop: func() *routepb.FIBNexthop {
				result := validNexthop()
				result.Counter = counterPrefix + "packets"
				return result
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.nexthop.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
