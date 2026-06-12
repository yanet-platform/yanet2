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
