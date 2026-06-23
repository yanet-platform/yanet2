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
	routeFor := func(addr, sourceMAC, destinationMAC, device string) {
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
	routeFor := func(addr, sourceMAC, destinationMAC, device string) {
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
	routeFor := func(addr, sourceMAC, destinationMAC, device string) {
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

// Test_BuildFIB_PerGatewayDeviceFilter verifies that best-per-source
// selection runs over the gateway's device-scoped local RIB.
//
// A gateway whose device set excludes the globally best route must fall
// back to a lower-priority route on one of its own devices rather than
// being starved.
func Test_BuildFIB_PerGatewayDeviceFilter(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(addr, sourceMAC, destinationMAC, device string) {
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
			// Globally best bird route (Pref=200) egresses through eth2.
			{NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 200},
			// Worse bird route (Pref=100) egresses through eth1.
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: rib.RouteSourceBird, Pref: 100},
		},
	}

	tests := []struct {
		name       string
		devices    []string
		wantDevice string
	}{
		{
			name:       "no filter selects the global best on eth2",
			devices:    nil,
			wantDevice: "eth2",
		},
		{
			name:       "gateway without eth2 falls back to its eth1 route",
			devices:    []string{"eth1"},
			wantDevice: "eth1",
		},
		{
			name:       "gateway with eth2 keeps the global best",
			devices:    []string{"eth2"},
			wantDevice: "eth2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			view := neigh.FilterByDevices(cache.View(), tc.devices)
			fib, _ := BuildFIB(ribDump, view)
			require.Len(t, fib.Entries, 1)
			require.Len(t, fib.Entries[0].Nexthops, 1)
			require.Equal(t, tc.wantDevice, fib.Entries[0].Nexthops[0].Device)
		})
	}
}

// Test_BuildFIB_DeviceFilterStarvesPrefix verifies that a prefix with no
// route on any of the gateway's devices is omitted entirely.
func Test_BuildFIB_DeviceFilterStarvesPrefix(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	cache.Set(netip.MustParseAddr("10.0.0.1"), neigh.NeighbourEntry{
		HardwareRoute: neigh.HardwareRoute{
			SourceMAC:      mustParseMAC(t, "0a:00:00:00:00:01"),
			DestinationMAC: mustParseMAC(t, "0a:00:00:00:10:00"),
			Device:         "eth1",
		},
	})

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: netip.MustParseAddr("192.0.2.1"), SourceID: rib.RouteSourceBird},
		},
	}

	view := neigh.FilterByDevices(cache.View(), []string{"eth2"})
	fib, stats := BuildFIB(ribDump, view)
	require.Empty(t, fib.Entries)
	require.Equal(t, 1, stats.NeighbourNotFound)
}

// Test_BuildFIB_FallsBackToLiveRouteWhenBestUnresolvable verifies that an
// unresolvable best route yields to a worse but resolvable route of the
// same source.
//
// This is the live-best behaviour and fires independently of any device
// filter, so the device set is left empty here.
func Test_BuildFIB_FallsBackToLiveRouteWhenBestUnresolvable(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	// Only the worse route's nexthop is resolvable; the best route's
	// nexthop has no neighbour entry.
	cache.Set(netip.MustParseAddr("10.0.0.1"), neigh.NeighbourEntry{
		HardwareRoute: neigh.HardwareRoute{
			SourceMAC:      mustParseMAC(t, "0a:00:00:00:00:01"),
			DestinationMAC: mustParseMAC(t, "0a:00:00:00:10:00"),
			Device:         "eth1",
		},
	})

	p1 := netip.MustParseAddr("192.0.2.1")
	p2 := netip.MustParseAddr("192.0.2.2")

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			// Best bird route (Pref=200) — its nexthop is unresolvable.
			{NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 200},
			// Worse bird route (Pref=100) — resolvable on eth1.
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: rib.RouteSourceBird, Pref: 100},
		},
	}

	fib, stats := BuildFIB(ribDump, cache.View())

	require.Len(t, fib.Entries, 1)
	require.Len(t, fib.Entries[0].Nexthops, 1)
	require.Equal(t, "eth1", fib.Entries[0].Nexthops[0].Device)
	require.Equal(t, 1, stats.NeighbourNotFound)
	require.Equal(t, 0, stats.FilteredRoutes, "the single live local route is its source's best")
}

// Test_BuildFIB_MultiSourceWithDeviceFilter verifies that best-per-source
// selection is scoped to the gateway's devices.
//
// A source whose best route is on an excluded device falls back to its
// route on an owned device, while a different source on an owned device is
// kept independently.
func Test_BuildFIB_MultiSourceWithDeviceFilter(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(addr, sourceMAC, destinationMAC, device string) {
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
	routeFor("10.0.0.3", "0a:00:00:00:00:03", "0a:00:00:00:30:00", "eth1")

	p2 := netip.MustParseAddr("192.0.2.2")
	p3 := netip.MustParseAddr("192.0.2.3")
	unspec := netip.IPv6Unspecified()

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			// Bird best (Pref=200) egresses through the excluded eth2.
			{NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 200},
			// Bird fallback (Pref=100) egresses through the owned eth1.
			{NextHop: netip.MustParseAddr("10.0.0.3"), Peer: p3, SourceID: rib.RouteSourceBird, Pref: 100},
			// Static route egresses through the owned eth1.
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: unspec, SourceID: rib.RouteSourceStatic, Pref: 100},
		},
	}

	view := neigh.FilterByDevices(cache.View(), []string{"eth1"})
	fib, stats := BuildFIB(ribDump, view)

	require.Len(t, fib.Entries, 1)
	require.Len(t, fib.Entries[0].Nexthops, 2, "bird fallback and static, both on eth1")
	for _, nh := range fib.Entries[0].Nexthops {
		require.Equal(t, "eth1", nh.Device)
	}
	require.Equal(t, 1, stats.NeighbourNotFound, "the bird route on eth2 is dropped")
}

// Test_BuildFIB_MultiPrefix verifies that each prefix gets its own
// nexthops and that the per-pass counters accumulate across prefixes.
//
// The two prefixes have different surviving-route counts and live at
// different prefix lengths, so any cross-prefix bleed would corrupt the
// smaller prefix's result.
func Test_BuildFIB_MultiPrefix(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(addr, sourceMAC, destinationMAC, device string) {
		cache.Set(netip.MustParseAddr(addr), neigh.NeighbourEntry{
			HardwareRoute: neigh.HardwareRoute{
				SourceMAC:      mustParseMAC(t, sourceMAC),
				DestinationMAC: mustParseMAC(t, destinationMAC),
				Device:         device,
			},
		})
	}

	routeFor("10.0.0.1", "0a:00:00:00:00:01", "0a:00:00:00:10:00", "eth1")
	routeFor("10.0.0.2", "0a:00:00:00:00:02", "0a:00:00:00:20:00", "eth1")
	routeFor("10.0.0.3", "0a:00:00:00:00:03", "0a:00:00:00:30:00", "eth2")
	routeFor("10.1.0.1", "0a:00:00:00:00:04", "0a:00:00:00:40:00", "eth1")

	p1 := netip.MustParseAddr("192.0.2.1")
	p2 := netip.MustParseAddr("192.0.2.2")
	p3 := netip.MustParseAddr("192.0.2.3")
	p4 := netip.MustParseAddr("192.0.2.4")

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	// Wider prefix: three equal-cost bird routes; one egresses through the
	// excluded eth2, leaving two ECMP nexthops on eth1.
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: rib.RouteSourceBird, Pref: 100},
			{NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 100},
			{NextHop: netip.MustParseAddr("10.0.0.3"), Peer: p3, SourceID: rib.RouteSourceBird, Pref: 100},
		},
	}
	// Narrower prefix at a different length: a single live route on eth1.
	ribDump[16][netip.MustParsePrefix("10.1.0.0/16")] = rib.RoutesList{
		Routes: []rib.Route{
			{NextHop: netip.MustParseAddr("10.1.0.1"), Peer: p4, SourceID: rib.RouteSourceBird, Pref: 100},
		},
	}

	view := neigh.FilterByDevices(cache.View(), []string{"eth1"})
	fib, stats := BuildFIB(ribDump, view)

	require.Len(t, fib.Entries, 2)
	require.Equal(t, 1, stats.NeighbourNotFound, "only the eth2 route of the wider prefix is dropped")

	byPrefix := map[string]FIBEntry{}
	for _, entry := range fib.Entries {
		byPrefix[entry.Prefix.String()] = entry
	}

	wide, ok := byPrefix["10.0.0.0/24"]
	require.True(t, ok)
	require.Len(t, wide.Nexthops, 2, "two eth1 ECMP nexthops survive the device filter")
	for _, nh := range wide.Nexthops {
		require.Equal(t, "eth1", nh.Device)
	}

	narrow, ok := byPrefix["10.1.0.0/16"]
	require.True(t, ok)
	require.Len(t, narrow.Nexthops, 1, "buffer reuse must not leak the wider prefix's routes")
	require.Equal(t, "eth1", narrow.Nexthops[0].Device)
}

func Test_BuildFIB_DedupsNexthops(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(addr, sourceMAC, destinationMAC, device string) {
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
