package operator_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

// Test_BuildFIB_StaticDeviceConstraint verifies that gateway ownership and
// configured egress are both applied before neighbour priority selection.
func Test_BuildFIB_StaticDeviceConstraint(t *testing.T) {
	tests := []struct {
		name           string
		devices        []string
		routeDevice    string
		expectedDevice string
	}{
		{
			name:           "unrestricted route keeps the highest priority neighbour",
			expectedDevice: "device0",
		},
		{
			name:           "bound route selects its lower priority neighbour",
			devices:        []string{"device0", "device1"},
			routeDevice:    "device1",
			expectedDevice: "device1",
		},
		{
			name:           "unrestricted gateway still honors configured egress",
			routeDevice:    "device1",
			expectedDevice: "device1",
		},
		{
			name:        "bound route cannot escape gateway ownership",
			devices:     []string{"device0"},
			routeDevice: "device1",
		},
		{
			name:        "missing bound neighbour cannot fall back to another link",
			routeDevice: "missing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nextHop := netip.MustParseAddr("fe80::1")
			table := neigh.NewNeighTable()
			for idx, device := range []string{"device0", "device1"} {
				_, err := table.CreateSource(device, uint32(100+idx), false)
				require.NoError(t, err)
				require.NoError(t, table.Add(device, []neigh.NeighbourEntry{{
					NextHop: nextHop,
					HardwareRoute: neigh.HardwareRoute{
						Device: device,
					},
				}}))
			}
			prefix := netip.MustParsePrefix("2001:db8::/64")
			dump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](1)
			dump[prefix.Bits()][prefix] = rib.RoutesList{Routes: []rib.Route{{
				Prefix:   prefix,
				NextHop:  nextHop,
				Device:   test.routeDevice,
				SourceID: rib.RouteSourceStatic,
			}}}

			fib, stats := operator.BuildFIB(dump, table.Snapshot(), test.devices)
			if test.expectedDevice == "" {
				require.Empty(t, fib.Entries)
				require.Equal(t, 1, stats.NeighbourNotFound)
				return
			}
			require.Len(t, fib.Entries, 1)
			require.Len(t, fib.Entries[0].Nexthops, 1)
			require.Equal(t, test.expectedDevice, fib.Entries[0].Nexthops[0].Device)
		})
	}
}
