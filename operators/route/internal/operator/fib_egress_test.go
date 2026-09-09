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

// Test_BuildFIB_RemoteScope verifies that equal link-local addresses resolve by
// publisher ifindex even when a static neighbour overrides the pair's MAC.
func Test_BuildFIB_RemoteScope(t *testing.T) {
	for _, test := range []struct {
		name           string
		firstIndex     uint32
		removeFirst    bool
		staticOverride bool
		wantFirst      bool
	}{
		{name: "two scoped devices", firstIndex: 10, wantFirst: true},
		{name: "static override retains remote binding", firstIndex: 10, staticOverride: true, wantFirst: true},
		{name: "missing pair never uses other device", firstIndex: 10, removeFirst: true},
		{name: "static override cannot replace missing remote binding", firstIndex: 10, removeFirst: true, staticOverride: true},
		{name: "missing explicit index never falls back", firstIndex: 99},
		{name: "zero index cannot choose ambiguous device"},
		{name: "unambiguous legacy route", removeFirst: true, wantFirst: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := neigh.NewNeighTable()
			address := netip.MustParseAddr("fe80::1")
			first := neigh.NeighbourEntry{NextHop: address, Ifindex: 10, HardwareRoute: neigh.HardwareRoute{Device: "logical0", DestinationMAC: [6]byte{2, 0, 0, 0, 0, 1}}}
			second := first
			second.Ifindex, second.HardwareRoute.Device, second.HardwareRoute.DestinationMAC[5] = 20, "logical1", 2
			input := map[neigh.Key]neigh.NeighbourEntry{first.Key(): first, second.Key(): second}
			if test.removeFirst {
				delete(input, first.Key())
			}
			_, err := table.ReplaceSource(t.Context(), "remote", 100, input)
			require.NoError(t, err)
			if test.staticOverride {
				_, err := table.CreateSource("static", 10, true)
				require.NoError(t, err)
				first.Ifindex, first.HardwareRoute.DestinationMAC[5] = 0, 3
				require.NoError(t, table.Add("static", []neigh.NeighbourEntry{first}))
			}
			dump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
			prefix := netip.MustParsePrefix("2001:db8:1::/64")
			other := netip.MustParsePrefix("2001:db8:2::/64")
			dump[64][prefix] = rib.RoutesList{Routes: []rib.Route{{NextHop: address, Ifindex: test.firstIndex, SourceID: rib.RouteSourceBird}}}
			dump[64][other] = rib.RoutesList{Routes: []rib.Route{{NextHop: address, Ifindex: 20, SourceID: rib.RouteSourceBird}}}
			fib, _ := operator.BuildFIB(dump, table.Snapshot(), []string{"logical0", "logical1"}, operator.WithFIBScopeSource("remote"))
			actual := map[netip.Prefix][]neigh.HardwareRoute{}
			for _, entry := range fib.Entries {
				actual[entry.Prefix] = entry.Nexthops
			}
			require.Equal(t, []neigh.HardwareRoute{second.HardwareRoute}, actual[other])
			if !test.wantFirst {
				require.NotContains(t, actual, prefix)
				return
			}
			wanted := first.HardwareRoute
			if test.removeFirst {
				wanted = second.HardwareRoute
			}
			require.Equal(t, []neigh.HardwareRoute{wanted}, actual[prefix])
		})
	}
}
