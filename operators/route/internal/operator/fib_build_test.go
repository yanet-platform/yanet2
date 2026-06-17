package operator

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

func mustParseMAC(t *testing.T, value string) [6]byte {
	t.Helper()

	mac, err := net.ParseMAC(value)
	require.NoError(t, err)
	require.Len(t, mac, 6)

	return [6]byte(mac)
}

// Test_BuildFIB_BestPerSourceFiltersWorse verifies that lower-cost routes from
// the same source are excluded when a strictly better route exists for that source.
func Test_BuildFIB_BestPerSourceFiltersWorse(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(addr, sourceMAC, destinationMAC string, device string) {
		cache.Set(netip.MustParseAddr(addr), neigh.NeighbourEntry{
			HardwareRoute: neigh.HardwareRoute{
				SourceMAC:      mustParseMAC(t, sourceMAC),
				DestinationMAC: mustParseMAC(t, destinationMAC),
				Device:         device,
			},
		})
	}

	routeFor("10.0.0.1", "0a:00:00:00:00:01", "0a:00:00:00:10:00", "eth1")
	routeFor("10.0.0.2", "0a:00:00:00:00:02", "0a:00:00:00:20:00", "eth2")

	p1 := netip.MustParseAddr("192.0.2.1")
	p2 := netip.MustParseAddr("192.0.2.2")

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			// Best bird route (Pref=200) — should be in FIB.
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: rib.RouteSourceBird, Pref: 200},
			// Worse bird route (Pref=100) — should be filtered out.
			{NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 100},
		},
	}

	fib, stats := BuildFIB(ribDump, cache.View())

	require.Equal(t, 2, stats.TotalRoutes)
	require.Equal(t, 1, stats.FilteredRoutes)
	require.Len(t, fib.Entries, 1)
	require.Len(t, fib.Entries[0].Nexthops, 1)
}

// Test_BuildFIB_EqualCostECMPPreserved verifies that equal-cost routes from
// different peers are all included in the FIB as ECMP nexthops.
func Test_BuildFIB_EqualCostECMPPreserved(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(addr, sourceMAC, destinationMAC string, device string) {
		cache.Set(netip.MustParseAddr(addr), neigh.NeighbourEntry{
			HardwareRoute: neigh.HardwareRoute{
				SourceMAC:      mustParseMAC(t, sourceMAC),
				DestinationMAC: mustParseMAC(t, destinationMAC),
				Device:         device,
			},
		})
	}

	routeFor("10.0.0.1", "0a:00:00:00:00:01", "0a:00:00:00:10:00", "eth1")
	routeFor("10.0.0.2", "0a:00:00:00:00:02", "0a:00:00:00:20:00", "eth2")

	p1 := netip.MustParseAddr("192.0.2.1")
	p2 := netip.MustParseAddr("192.0.2.2")

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			// Both bird routes have equal cost — ECMP must be preserved.
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: rib.RouteSourceBird, Pref: 100},
			{NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 100},
		},
	}

	fib, stats := BuildFIB(ribDump, cache.View())

	require.Equal(t, 2, stats.TotalRoutes)
	require.Equal(t, 0, stats.FilteredRoutes)
	require.Len(t, fib.Entries, 1)
	require.Len(t, fib.Entries[0].Nexthops, 2)
}

// Test_BuildFIB_StaticAndBirdBothInFIB verifies that static and BGP routes for
// the same prefix each contribute their best nexthop to the FIB independently.
func Test_BuildFIB_StaticAndBirdBothInFIB(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(addr, sourceMAC, destinationMAC string, device string) {
		cache.Set(netip.MustParseAddr(addr), neigh.NeighbourEntry{
			HardwareRoute: neigh.HardwareRoute{
				SourceMAC:      mustParseMAC(t, sourceMAC),
				DestinationMAC: mustParseMAC(t, destinationMAC),
				Device:         device,
			},
		})
	}

	routeFor("10.0.0.1", "0a:00:00:00:00:01", "0a:00:00:00:10:00", "eth1")
	routeFor("10.0.0.2", "0a:00:00:00:00:02", "0a:00:00:00:20:00", "eth2")

	birdPeer := netip.MustParseAddr("192.0.2.1")
	unspec := netip.IPv6Unspecified()

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			// Bird route is best overall (Pref=200).
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: birdPeer, SourceID: rib.RouteSourceBird, Pref: 200},
			// Static route has lower overall Pref (100) but is its own source.
			{NextHop: netip.MustParseAddr("10.0.0.2"), Peer: unspec, SourceID: rib.RouteSourceStatic, Pref: 100},
		},
	}

	fib, stats := BuildFIB(ribDump, cache.View())

	require.Equal(t, 2, stats.TotalRoutes)
	require.Equal(t, 0, stats.FilteredRoutes, "static route is its own source's best — not filtered")
	require.Len(t, fib.Entries, 1)
	require.Len(t, fib.Entries[0].Nexthops, 2, "both sources' nexthops should be in FIB")
}

// Test_FilterFIBEntries verifies device-scoped nexthop filtering.
func Test_FilterFIBEntries(t *testing.T) {
	mac1 := mustParseMAC(t, "0a:00:00:00:00:01")
	mac2 := mustParseMAC(t, "0a:00:00:00:00:02")
	mac3 := mustParseMAC(t, "0a:00:00:00:00:03")

	nh := func(device string, src [6]byte) neigh.HardwareRoute {
		return neigh.HardwareRoute{SourceMAC: src, Device: device}
	}

	entries := []FIBEntry{
		{
			Prefix:   netip.MustParsePrefix("10.0.0.0/24"),
			Nexthops: []neigh.HardwareRoute{nh("eth0", mac1), nh("eth1", mac2)},
		},
		{
			Prefix:   netip.MustParsePrefix("10.0.1.0/24"),
			Nexthops: []neigh.HardwareRoute{nh("eth1", mac3)},
		},
	}

	tests := []struct {
		name            string
		devices         map[string]struct{}
		wantPrefixes    []string
		wantNexthopDevs [][]string
	}{
		{
			name:            "empty device set returns all entries unchanged",
			devices:         map[string]struct{}{},
			wantPrefixes:    []string{"10.0.0.0/24", "10.0.1.0/24"},
			wantNexthopDevs: [][]string{{"eth0", "eth1"}, {"eth1"}},
		},
		{
			name:            "one entry dropped when its nexthops are all on other devices",
			devices:         map[string]struct{}{"eth0": {}},
			wantPrefixes:    []string{"10.0.0.0/24"},
			wantNexthopDevs: [][]string{{"eth0"}},
		},
		{
			name:            "all entries dropped when device owns no nexthops",
			devices:         map[string]struct{}{"eth2": {}},
			wantPrefixes:    []string{},
			wantNexthopDevs: [][]string{},
		},
	}

	// Capture nexthop counts before any call to detect mutations of the input.
	nexthopCounts := make([]int, len(entries))
	for idx := range entries {
		nexthopCounts[idx] = len(entries[idx].Nexthops)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterFIBEntries(entries, tc.devices)
			require.Len(t, got, len(tc.wantPrefixes))
			for idx, wantPrefix := range tc.wantPrefixes {
				require.Equal(t, netip.MustParsePrefix(wantPrefix), got[idx].Prefix)
				devs := make([]string, len(got[idx].Nexthops))
				for hrIdx, hr := range got[idx].Nexthops {
					devs[hrIdx] = hr.Device
				}
				require.Equal(t, tc.wantNexthopDevs[idx], devs)
			}
			// Verify the input entries were not mutated.
			for idx := range entries {
				require.Len(t, entries[idx].Nexthops, nexthopCounts[idx])
			}
		})
	}
}

func Test_BuildFIB_DedupsNexthops(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(addr, sourceMAC, destinationMAC string, device string) {
		cache.Set(netip.MustParseAddr(addr), neigh.NeighbourEntry{
			HardwareRoute: neigh.HardwareRoute{
				SourceMAC:      mustParseMAC(t, sourceMAC),
				DestinationMAC: mustParseMAC(t, destinationMAC),
				Device:         device,
			},
		})
	}

	routeFor("10.0.0.1", "0a:00:00:00:00:01", "0a:00:00:00:10:00", "eth2")
	routeFor("10.0.0.2", "09:00:00:00:00:00", "0a:00:00:00:20:00", "eth1")
	routeFor("10.0.0.3", "0a:00:00:00:00:01", "0a:00:00:00:10:10", "eth1")
	routeFor("10.0.0.4", "09:00:00:00:00:00", "0a:00:00:00:20:00", "eth1")

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			{NextHop: netip.MustParseAddr("10.0.0.1")},
			{NextHop: netip.MustParseAddr("10.0.0.2")},
			{NextHop: netip.MustParseAddr("10.0.0.3")},
			{NextHop: netip.MustParseAddr("10.0.0.4")},
		},
	}

	fib, stats := BuildFIB(ribDump, cache.View())

	require.Len(t, fib.Entries, 1)
	require.Equal(t, 1, stats.PrefixesAdded)
	require.Equal(t, 1, stats.TotalPrefixes)
	require.Equal(t, 4, stats.TotalRoutes)
	require.Equal(t, 0, stats.NeighbourNotFound)
	require.Equal(t, 3, stats.HardwareRoutes)

	expected := []neigh.HardwareRoute{
		{
			SourceMAC:      mustParseMAC(t, "09:00:00:00:00:00"),
			DestinationMAC: mustParseMAC(t, "0a:00:00:00:20:00"),
			Device:         "eth1",
		},
		{
			SourceMAC:      mustParseMAC(t, "0a:00:00:00:00:01"),
			DestinationMAC: mustParseMAC(t, "0a:00:00:00:10:00"),
			Device:         "eth2",
		},
		{
			SourceMAC:      mustParseMAC(t, "0a:00:00:00:00:01"),
			DestinationMAC: mustParseMAC(t, "0a:00:00:00:10:10"),
			Device:         "eth1",
		},
	}
	require.Equal(t, expected, fib.Entries[0].Nexthops)
}
