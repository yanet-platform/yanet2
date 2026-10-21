package operator

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// mustParseMAC returns an EUI-48 fixture address, rejecting invalid literals.
func mustParseMAC(t *testing.T, value string) [6]byte {
	t.Helper()

	mac, err := net.ParseMAC(value)
	require.NoError(t, err)
	require.Len(t, mac, 6)

	return [6]byte(mac)
}

// Test_BuildFIB_PerGatewayDeviceFilter verifies that best-per-source
// selection runs over the gateway's device-scoped local RIB.
//
// A gateway whose device set excludes the globally best route must fall
// back to a lower-priority route on one of its own devices rather than
// being starved.
func Test_BuildFIB_PerGatewayDeviceFilter(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(address, sourceMAC, destinationMAC, device string) neigh.HardwareRoute {
		hardwareRoute := neigh.HardwareRoute{
			SourceMAC:      mustParseMAC(t, sourceMAC),
			DestinationMAC: mustParseMAC(t, destinationMAC),
			Device:         device,
		}
		cache.Set(netip.MustParseAddr(address), neigh.NeighbourEntry{HardwareRoute: hardwareRoute})
		return hardwareRoute
	}

	local := routeFor("10.0.0.1", "0a:00:00:00:00:01", "0a:00:00:00:10:00", "eth1")
	best := routeFor("10.0.0.2", "0a:00:00:00:00:02", "0a:00:00:00:20:00", "eth2")

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
		name         string
		devices      []string
		wantRoute    neigh.HardwareRoute
		wantExcluded int
		wantFiltered int
	}{
		{
			name:         "no filter selects the global best on eth2",
			devices:      nil,
			wantRoute:    best,
			wantFiltered: 1,
		},
		{
			name:         "gateway without eth2 falls back to its eth1 route",
			devices:      []string{"eth1"},
			wantRoute:    local,
			wantExcluded: 1,
		},
		{
			name:         "gateway with eth2 keeps the global best",
			devices:      []string{"eth2"},
			wantRoute:    best,
			wantExcluded: 1,
		},
		{
			name:    "multiple owned devices select the global best",
			devices: []string{"eth1", "eth2"}, wantRoute: best, wantFiltered: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fib, stats := BuildFIB(ribDump, cache.View(), neigh.NewDeviceScope(tc.devices))
			require.Len(t, fib.Entries, 1)
			require.Equal(t, netip.MustParsePrefix("10.0.0.0/24"), fib.Entries[0].Prefix)
			require.Equal(t, []neigh.HardwareRoute{tc.wantRoute}, fib.Entries[0].Nexthops)
			require.Zero(t, stats.NeighbourNotFound)
			require.Equal(t, tc.wantExcluded, stats.DeviceExcludedRoutes)
			require.Equal(t, tc.wantFiltered, stats.FilteredRoutes)
			require.Equal(t, 2, stats.BirdRoutes)
			require.Equal(t, 2-tc.wantExcluded, stats.LocalBirdRoutes)
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

	fib, stats := BuildFIB(ribDump, cache.View(), neigh.NewDeviceScope([]string{"eth2"}))
	require.Empty(t, fib.Entries)
	require.Zero(t, stats.NeighbourNotFound)
	require.Equal(t, 1, stats.DeviceExcludedRoutes)
	require.Equal(t, 1, stats.BirdRoutes)
	require.Zero(t, stats.LocalBirdRoutes)
}

// Test_BuildFIB_FallsBackToLiveRouteWhenBestUnresolvable verifies that an
// unresolvable best route yields to a worse but resolvable route of the
// same source.
//
// This is the live-best behaviour and fires independently of any device
// filter, so the device set is left empty here.
func Test_BuildFIB_FallsBackToLiveRouteWhenBestUnresolvable(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	// Only the worse route's nexthop is resolvable — the best route's
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

	fib, stats := BuildFIB(ribDump, cache.View(), neigh.DeviceScope{})

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
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: unspec, SourceID: rib.RouteSourceStatic, Pref: 50},
		},
	}

	fib, stats := BuildFIB(ribDump, cache.View(), neigh.NewDeviceScope([]string{"eth1"}))

	require.Len(t, fib.Entries, 1)
	require.ElementsMatch(t, []neigh.HardwareRoute{
		{
			SourceMAC:      mustParseMAC(t, "0a:00:00:00:00:01"),
			DestinationMAC: mustParseMAC(t, "0a:00:00:00:10:00"), Device: "eth1",
		},
		{
			SourceMAC:      mustParseMAC(t, "0a:00:00:00:00:03"),
			DestinationMAC: mustParseMAC(t, "0a:00:00:00:30:00"), Device: "eth1",
		},
	}, fib.Entries[0].Nexthops)
	require.Zero(t, stats.NeighbourNotFound)
	require.Equal(t, 1, stats.DeviceExcludedRoutes, "the bird route on eth2 is dropped")
	require.Equal(t, 2, stats.BirdRoutes)
	require.Equal(t, 1, stats.LocalBirdRoutes)
}

// Test_BuildFIB_MultiPrefix verifies that nexthops remain prefix-local while
// missing and excluded route counts accumulate across different prefix lengths.
func Test_BuildFIB_MultiPrefix(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	routeFor := func(address, sourceMAC, destinationMAC, device string) neigh.HardwareRoute {
		hardwareRoute := neigh.HardwareRoute{
			SourceMAC:      mustParseMAC(t, sourceMAC),
			DestinationMAC: mustParseMAC(t, destinationMAC),
			Device:         device,
		}
		cache.Set(netip.MustParseAddr(address), neigh.NeighbourEntry{HardwareRoute: hardwareRoute})
		return hardwareRoute
	}

	first := routeFor("10.0.0.1", "0a:00:00:00:00:01", "0a:00:00:00:10:00", "eth1")
	second := routeFor("10.0.0.2", "0a:00:00:00:00:02", "0a:00:00:00:20:00", "eth1")
	routeFor("10.0.0.3", "0a:00:00:00:00:03", "0a:00:00:00:30:00", "eth2")
	third := routeFor("10.1.0.1", "0a:00:00:00:00:04", "0a:00:00:00:40:00", "eth1")

	p1 := netip.MustParseAddr("192.0.2.1")
	p2 := netip.MustParseAddr("192.0.2.2")
	p3 := netip.MustParseAddr("192.0.2.3")
	p4 := netip.MustParseAddr("192.0.2.4")

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			{NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: rib.RouteSourceBird, Pref: 100},
			{NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 100},
			{NextHop: netip.MustParseAddr("10.0.0.3"), Peer: p3, SourceID: rib.RouteSourceBird, Pref: 100},
			{NextHop: netip.MustParseAddr("10.0.0.4"), Peer: p4, SourceID: rib.RouteSourceBird, Pref: 100},
		},
	}
	ribDump[16][netip.MustParsePrefix("10.1.0.0/16")] = rib.RoutesList{
		Routes: []rib.Route{
			{NextHop: netip.MustParseAddr("10.1.0.1"), Peer: p4, SourceID: rib.RouteSourceBird, Pref: 100},
			{NextHop: netip.MustParseAddr("10.0.0.3"), Peer: p3, SourceID: rib.RouteSourceBird, Pref: 100},
			{NextHop: netip.MustParseAddr("10.1.0.2"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 100},
		},
	}

	fib, stats := BuildFIB(ribDump, cache.View(), neigh.NewDeviceScope([]string{"eth1"}))

	require.ElementsMatch(t, []FIBEntry{
		{Prefix: netip.MustParsePrefix("10.0.0.0/24"), Nexthops: []neigh.HardwareRoute{first, second}},
		{Prefix: netip.MustParsePrefix("10.1.0.0/16"), Nexthops: []neigh.HardwareRoute{third}},
	}, fib.Entries)
	require.Equal(t, FIBBuildStats{
		TotalPrefixes: 2, TotalRoutes: 7, NeighbourNotFound: 2,
		BirdRoutes: 7, LocalBirdRoutes: 3,
		DeviceExcludedRoutes: 2, HardwareRoutes: 3, PrefixesAdded: 2,
	}, stats)
}

// Test_BuildFIB_MixedEligibility verifies that route-record classifications stay
// separate from local cost selection, ECMP, and hardware-route deduplication.
func Test_BuildFIB_MixedEligibility(t *testing.T) {
	first := neigh.HardwareRoute{
		SourceMAC:      [6]byte{2, 0, 0, 0, 0, 1},
		DestinationMAC: [6]byte{2, 0, 0, 0, 0, 2}, Device: "eth1",
	}
	second := first
	second.DestinationMAC[5] = 3
	worse := second
	worse.DestinationMAC[5] = 4
	foreign := first
	foreign.Device = "eth2"
	cache := rcucache.NewCache(map[netip.Addr]neigh.NeighbourEntry{
		netip.MustParseAddr("192.0.2.1"): {HardwareRoute: first},
		netip.MustParseAddr("192.0.2.2"): {HardwareRoute: first},
		netip.MustParseAddr("192.0.2.3"): {HardwareRoute: second},
		netip.MustParseAddr("192.0.2.4"): {HardwareRoute: worse},
		netip.MustParseAddr("192.0.2.5"): {HardwareRoute: foreign},
	})
	list := rib.RoutesList{}
	for idx, path := range []struct {
		nexthop    string
		preference uint32
	}{
		{nexthop: "192.0.2.1", preference: 100},
		{nexthop: "::ffff:192.0.2.2", preference: 100},
		{nexthop: "192.0.2.3", preference: 100},
		{nexthop: "192.0.2.4", preference: 50},
		{nexthop: "192.0.2.5", preference: 200},
		{nexthop: "::ffff:192.0.2.5", preference: 200},
		{nexthop: "192.0.2.6", preference: 300},
		{nexthop: "::ffff:192.0.2.6", preference: 300},
	} {
		list.Insert(rib.Route{
			NextHop: netip.MustParseAddr(path.nexthop), Pref: path.preference,
			SourceID: rib.RouteSourceBird, GlobalID: uint32(idx + 1),
		})
	}
	list.Insert(rib.Route{
		NextHop: netip.MustParseAddr("192.0.2.1"), Pref: 500,
		SourceID: rib.RouteSourceStatic,
	})
	prefix := netip.MustParsePrefix("10.0.0.0/24")
	dump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](1)
	dump[prefix.Bits()][prefix] = list

	fib, stats := BuildFIB(dump, cache.View(), neigh.NewDeviceScope([]string{"eth1"}))

	require.Len(t, fib.Entries, 1)
	require.Equal(t, prefix, fib.Entries[0].Prefix)
	require.ElementsMatch(t, []neigh.HardwareRoute{first, second}, fib.Entries[0].Nexthops)
	require.Equal(t, FIBBuildStats{
		TotalPrefixes: 1, TotalRoutes: 9, NeighbourNotFound: 2,
		BirdRoutes: 8, LocalBirdRoutes: 4,
		DeviceExcludedRoutes: 2, HardwareRoutes: 2, PrefixesAdded: 1,
		FilteredRoutes: 1,
	}, stats)
}

// Test_BuildFIB_MissingNeighbourCountsRouteRecords verifies that a shared missing
// nexthop counts every route record even when the resulting table is empty.
func Test_BuildFIB_MissingNeighbourCountsRouteRecords(t *testing.T) {
	cache := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	dump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](1)
	dump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			{NextHop: netip.MustParseAddr("192.0.2.1"), SourceID: rib.RouteSourceBird, GlobalID: 1},
			{NextHop: netip.MustParseAddr("192.0.2.1"), SourceID: rib.RouteSourceBird, GlobalID: 2},
		},
	}
	fib, stats := BuildFIB(dump, cache.View(), neigh.NewDeviceScope([]string{"eth1"}))
	require.Empty(t, fib.Entries)
	require.Equal(t, 2, stats.NeighbourNotFound)
	require.Equal(t, 2, stats.BirdRoutes)
	require.Zero(t, stats.LocalBirdRoutes)
	require.Zero(t, stats.DeviceExcludedRoutes)
	require.Zero(t, stats.FilteredRoutes)
}

// Test_BuildFIB_DedupsNexthops verifies that nonadjacent routes resolving to the
// same hardware path produce one nexthop without discarding distinct paths.
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
	routeFor("10.0.0.3", "0a:00:00:00:00:01", "0a:00:00:00:10:00", "eth1")
	routeFor("10.0.0.4", "09:00:00:00:00:00", "0a:00:00:00:20:00", "eth1")

	ribDump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](2)
	ribDump[24][netip.MustParsePrefix("10.0.0.0/24")] = rib.RoutesList{
		Routes: []rib.Route{
			{NextHop: netip.MustParseAddr("10.0.0.1"), SourceID: rib.RouteSourceStatic},
			{NextHop: netip.MustParseAddr("10.0.0.2"), SourceID: rib.RouteSourceStatic},
			{NextHop: netip.MustParseAddr("10.0.0.3"), SourceID: rib.RouteSourceStatic},
			{NextHop: netip.MustParseAddr("10.0.0.4"), SourceID: rib.RouteSourceStatic},
		},
	}

	fib, stats := BuildFIB(ribDump, cache.View(), neigh.DeviceScope{})

	require.Len(t, fib.Entries, 1)
	require.Zero(t, stats.BirdRoutes)
	require.Zero(t, stats.LocalBirdRoutes)

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
			DestinationMAC: mustParseMAC(t, "0a:00:00:00:10:00"),
			Device:         "eth1",
		},
	}
	require.ElementsMatch(t, expected, fib.Entries[0].Nexthops)
}
