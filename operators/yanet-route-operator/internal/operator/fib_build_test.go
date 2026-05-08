package operator

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/rib"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/common/go/rcucache"
)

func mustParseMAC(t *testing.T, value string) [6]byte {
	t.Helper()

	mac, err := net.ParseMAC(value)
	require.NoError(t, err)
	require.Len(t, mac, 6)

	return [6]byte(mac)
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
