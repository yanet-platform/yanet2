package operatorpb_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// Test_NeighbourEntry_Validate verifies that both MAC address fields are
// required while next-hop parsing remains the handler's responsibility.
//
// A MAC address wider than EUI-48 is rejected.
func Test_NeighbourEntry_Validate(t *testing.T) {
	cases := []struct {
		name    string
		entry   *operatorpb.NeighbourEntry
		message string
	}{
		{
			name: "missing hardware address",
			entry: &operatorpb.NeighbourEntry{
				LinkAddr: &commonpb.MACAddress{},
			},
			message: "hardware_addr is required",
		},
		{
			name: "missing link address",
			entry: &operatorpb.NeighbourEntry{
				HardwareAddr: &commonpb.MACAddress{},
			},
			message: "link_addr is required",
		},
		{
			name: "both addresses present without next hop",
			entry: &operatorpb.NeighbourEntry{
				HardwareAddr: &commonpb.MACAddress{},
				LinkAddr:     &commonpb.MACAddress{},
			},
		},
		{
			name: "broadcast addresses",
			entry: &operatorpb.NeighbourEntry{
				HardwareAddr: &commonpb.MACAddress{Addr: 0xFFFF_FFFF_FFFF},
				LinkAddr:     &commonpb.MACAddress{Addr: 0xFFFF_FFFF_FFFF},
			},
		},
		{
			name: "hardware address wider than EUI-48",
			entry: &operatorpb.NeighbourEntry{
				HardwareAddr: &commonpb.MACAddress{Addr: 1 << 48},
				LinkAddr:     &commonpb.MACAddress{},
			},
			message: "hardware_addr must be an EUI-48 address",
		},
		{
			name: "link address wider than EUI-48",
			entry: &operatorpb.NeighbourEntry{
				HardwareAddr: &commonpb.MACAddress{},
				LinkAddr:     &commonpb.MACAddress{Addr: 1 << 48},
			},
			message: "link_addr must be an EUI-48 address",
		},
		{name: "nil entry", entry: nil, message: "hardware_addr is required"},
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

// Test_UpdateNeighboursRequest_Validate verifies that nested neighbour
// failures carry the repeated entry field path.
func Test_UpdateNeighboursRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		request *operatorpb.UpdateNeighboursRequest
		message string
	}{
		{name: "no entries", request: &operatorpb.UpdateNeighboursRequest{}},
		{
			name: "missing hardware address at index zero",
			request: &operatorpb.UpdateNeighboursRequest{
				Entries: []*operatorpb.NeighbourEntry{
					{LinkAddr: &commonpb.MACAddress{}},
				},
			},
			message: "entries[0]: hardware_addr is required",
		},
		{
			name: "missing link address at index one",
			request: &operatorpb.UpdateNeighboursRequest{
				Entries: []*operatorpb.NeighbourEntry{
					{
						HardwareAddr: &commonpb.MACAddress{},
						LinkAddr:     &commonpb.MACAddress{},
					},
					{HardwareAddr: &commonpb.MACAddress{}},
				},
			},
			message: "entries[1]: link_addr is required",
		},
		{
			name: "all addresses present without next hops",
			request: &operatorpb.UpdateNeighboursRequest{
				Entries: []*operatorpb.NeighbourEntry{
					{
						HardwareAddr: &commonpb.MACAddress{},
						LinkAddr:     &commonpb.MACAddress{},
					},
				},
			},
		},
		{name: "nil request", request: nil},
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
