package operator

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/rib"
)

func Test_RouteSource_WakeFuncCoalesces(t *testing.T) {
	source := NewRouteSource(
		neigh.NewNeighTable(),
		newRIBStore(zap.NewNop()),
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

func Test_RouteSource_SnapshotEmptyOK(t *testing.T) {
	source := NewRouteSource(
		neigh.NewNeighTable(),
		newRIBStore(zap.NewNop()),
	)

	fibs, ok := source.Snapshot()
	require.True(t, ok, "Snapshot must always return ok=true to keep the function publish alive")
	require.Empty(t, fibs)
}

func Test_RouteSource_SnapshotDrainsWake(t *testing.T) {
	source := NewRouteSource(
		neigh.NewNeighTable(),
		newRIBStore(zap.NewNop()),
	)

	source.WakeFunc()()
	_, _ = source.Snapshot()

	select {
	case <-source.Wake():
		t.Fatal("wake channel not drained by Snapshot")
	default:
	}
}

func Test_RouteSource_SnapshotIncludesRIBs(t *testing.T) {
	nd := neigh.NewNeighTable()
	nd.CreateSource(defaultStaticTable, 100, false)
	nd.Add(defaultStaticTable, []neigh.NeighbourEntry{
		{
			NextHop: netip.MustParseAddr("10.0.0.1"),
			HardwareRoute: neigh.HardwareRoute{
				SourceMAC:      [6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
				DestinationMAC: [6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
				Device:         "eth0",
			},
			UpdatedAt: time.Now(),
			State:     neigh.NeighbourStatePermanent,
			Source:    "kernel",
			Priority:  100,
		},
	})

	ribs := newRIBStore(zap.NewNop())
	source := NewRouteSource(nd, ribs)

	r := ribs.GetOrCreate("route0")
	r.AddUnicastRoute(
		netip.MustParsePrefix("10.0.0.0/24"),
		netip.MustParseAddr("10.0.0.1"),
		rib.RouteSourceStatic,
	)

	fibs, ok := source.Snapshot()
	require.True(t, ok)
	require.Len(t, fibs, 1)

	expected := FIB{
		Name: "route0",
		Entries: []FIBEntry{
			{
				Prefix: netip.MustParsePrefix("10.0.0.0/24"),
				Nexthops: []neigh.HardwareRoute{
					{
						SourceMAC:      [6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
						DestinationMAC: [6]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
						Device:         "eth0",
					},
				},
			},
		},
	}
	require.Equal(t, expected, fibs[0])
}
