package operator

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// Test_RouteSource_Build_IncludesRIBs verifies that the built desired state
// exposes the stored route paths together with their resolved neighbour
// entries.
func Test_RouteSource_Build_IncludesRIBs(t *testing.T) {
	neighbours := neigh.NewNeighTable()
	_, err := neighbours.CreateSource(defaultStaticTable, 100, false)
	require.NoError(t, err)
	entry := neigh.NeighbourEntry{
		NextHop: netip.MustParseAddr("10.0.0.1"),
		HardwareRoute: neigh.HardwareRoute{
			SourceMAC:      [6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
			DestinationMAC: [6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
			Device:         "eth0",
		},
		UpdatedAt: time.Now(),
		State:     neigh.NeighbourStatePermanent,
	}
	require.NoError(t, neighbours.Add(defaultStaticTable, []neigh.NeighbourEntry{entry}))

	ribs := newRIBStore()
	source := NewRouteSource(neighbours, ribs)
	prefix := netip.MustParsePrefix("10.0.0.0/24")
	ribs.GetOrCreate("route0").AddUnicastRoute(prefix, entry.NextHop, rib.RouteSourceStatic)

	snapshot := source.Build()
	require.Len(t, snapshot.RIBs, 1)
	require.Contains(t, snapshot.RIBs, "route0")
	paths := snapshot.RIBs["route0"][prefix.Bits()][prefix].Routes
	require.Len(t, paths, 1)
	require.Equal(t, entry.NextHop, paths[0].NextHop)
	require.Equal(t, rib.RouteSourceStatic, paths[0].SourceID)
	resolved, ok := snapshot.Neighbours.Lookup(entry.NextHop)
	require.True(t, ok)
	require.Equal(t, entry.HardwareRoute, resolved.HardwareRoute)
}

// Test_RouteSource_Changed_SeesUnreportedChange verifies that a RIB left as
// the last build saw it is not a change, while a route added after it is.
func Test_RouteSource_Changed_SeesUnreportedChange(t *testing.T) {
	ribs := newRIBStore()
	source := NewRouteSource(neigh.NewNeighTable(), ribs)
	holder := ribs.GetOrCreate("route0")
	source.Build()
	require.False(t, source.Changed())

	err := holder.AddUnicastRoute(
		netip.MustParsePrefix("10.0.0.0/24"),
		netip.MustParseAddr("10.0.0.1"),
		rib.RouteSourceStatic,
	)
	require.NoError(t, err)

	require.True(t, source.Changed())
}
