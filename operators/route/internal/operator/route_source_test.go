package operator

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

func Test_RouteSource_WakeFuncCoalesces(t *testing.T) {
	source := NewRouteSource(
		neigh.NewNeighTable(),
		newRIBStore(),
	)

	wake := source.WakeFunc()
	wake()
	wake()
	wake()

	select {
	case <-source.Wake():
	default:
		t.Fatal("wake channel not signalled")
	}

	select {
	case <-source.Wake():
		t.Fatal("wake channel signalled more than once")
	default:
	}
}

// Test_RouteSource_SnapshotEmptyOK verifies that empty desired state remains
// actionable for readiness failures and reconciliation retries.
func Test_RouteSource_SnapshotEmptyOK(t *testing.T) {
	source := NewRouteSource(
		neigh.NewNeighTable(),
		newRIBStore(),
	)

	snapshot, ok := source.Snapshot()
	require.True(t, ok, "empty desired state must remain actionable for readiness and retries")
	require.Empty(t, snapshot.RIBs)
}

func Test_RouteSource_SnapshotDrainsWake(t *testing.T) {
	source := NewRouteSource(
		neigh.NewNeighTable(),
		newRIBStore(),
	)

	source.WakeFunc()()
	_, _ = source.Snapshot()

	select {
	case <-source.Wake():
		t.Fatal("wake channel not drained by Snapshot")
	default:
	}
}

// Test_RouteSource_SnapshotIncludesRIBs verifies that snapshots expose the
// stored route paths together with their resolved neighbour entries.
func Test_RouteSource_SnapshotIncludesRIBs(t *testing.T) {
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

	snapshot, ok := source.Snapshot()
	require.True(t, ok)
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
