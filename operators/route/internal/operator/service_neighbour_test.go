package operator_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// newNeighbourFixture returns a neighbour service over a table whose change
// hook increments the returned counter.
func newNeighbourFixture(t *testing.T) (*operator.NeighbourService, *int) {
	t.Helper()

	fired := 0
	table := neigh.NewNeighTable(neigh.WithTableOnChanged(func() { fired++ }))
	_, err := table.CreateSource("static", 10, true)
	require.NoError(t, err)

	return operator.NewNeighbourService(table), &fired
}

// entryRequest builds an update request for one permanent neighbour.
func entryRequest(nexthop string, destinationMAC [6]byte, device string) *operatorpb.UpdateNeighboursRequest {
	return &operatorpb.UpdateNeighboursRequest{
		Entries: []*operatorpb.NeighbourEntry{{
			NextHop:      commonpb.NewIPAddressFromAddr(netip.MustParseAddr(nexthop)),
			LinkAddr:     commonpb.NewMACAddressEUI48(destinationMAC),
			HardwareAddr: commonpb.NewMACAddressEUI48([6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}),
			Device:       device,
		}},
	}
}

// Test_NeighbourService_UpdateNeighbours_WakesOnlyOnHardwareRouteChange
// verifies that a neighbour configured over the wire reaches the reconcile
// loop only when it changes a hardware route.
func Test_NeighbourService_UpdateNeighbours_WakesOnlyOnHardwareRouteChange(t *testing.T) {
	service, fired := newNeighbourFixture(t)
	mac := [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}

	_, err := service.UpdateNeighbours(t.Context(), entryRequest("10.0.0.1", mac, "eth0"))
	require.NoError(t, err)
	require.Equal(t, 1, *fired, "a newly configured neighbour must reach the reconcile loop")

	_, err = service.UpdateNeighbours(t.Context(), entryRequest("10.0.0.1", mac, "eth0"))
	require.NoError(t, err)
	require.Equal(t, 1, *fired, "re-sending an unchanged neighbour must not reach the reconcile loop")

	changed := [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x00}
	_, err = service.UpdateNeighbours(t.Context(), entryRequest("10.0.0.1", changed, "eth0"))
	require.NoError(t, err)
	require.Equal(t, 2, *fired, "a changed destination address must reach the reconcile loop")
}

// Test_NeighbourService_RemoveNeighbours_WakesOnlyWhenAnEntryGoesAway
// verifies that only a withdrawal that removes a nexthop reaches the loop.
func Test_NeighbourService_RemoveNeighbours_WakesOnlyWhenAnEntryGoesAway(t *testing.T) {
	service, fired := newNeighbourFixture(t)

	_, err := service.UpdateNeighbours(t.Context(), entryRequest("10.0.0.1", [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}, "eth0"))
	require.NoError(t, err)
	require.Equal(t, 1, *fired)

	unknown := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.2"))
	_, err = service.RemoveNeighbours(t.Context(), &operatorpb.RemoveNeighboursRequest{
		NextHops: []*commonpb.IPAddress{unknown},
	})
	require.NoError(t, err)
	require.Equal(t, 1, *fired, "withdrawing an unknown neighbour must not reach the reconcile loop")

	known := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))
	_, err = service.RemoveNeighbours(t.Context(), &operatorpb.RemoveNeighboursRequest{
		NextHops: []*commonpb.IPAddress{known},
	})
	require.NoError(t, err)
	require.Equal(t, 2, *fired, "withdrawing a configured neighbour must reach the reconcile loop")
}
