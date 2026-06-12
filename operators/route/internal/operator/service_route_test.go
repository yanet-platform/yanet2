package operator

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// TestShowRoutes_StaticECMP_BothBest verifies that two static ECMP nexthops
// for the same prefix both carry is_best=true.
func TestShowRoutes_StaticECMP_BothBest(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	nh1 := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))
	nh2 := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.2"))

	_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
		Name:         "route0",
		Prefix:       "10.0.0.0/24",
		NexthopAddrs: []*commonpb.IPAddress{nh1, nh2},
		SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
	})
	require.NoError(t, err)

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Len(t, resp.Routes, 2, "both ECMP nexthops must appear")
	for _, r := range resp.Routes {
		require.True(t, r.GetIsBest(), "static ECMP nexthop must have is_best=true")
	}
}

// TestShowRoutes_BirdDifferentPref_OnlyBetterIsBest verifies that when two
// bird routes for the same prefix differ in Pref, only the higher-Pref route
// carries is_best=true.
func TestShowRoutes_BirdDifferentPref_OnlyBetterIsBest(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	// Insert the lower-Pref route first so ordering is not insertion-order.
	_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
		Name:         "route0",
		Prefix:       "10.1.0.0/24",
		NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.2"))},
		SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
	})
	require.NoError(t, err)

	// Use the RIB directly to insert two bird routes with distinct Prefs via
	// the operator's RIB, accessed through the service internals.
	ribRef := svc.getOrCreateRib("route0")

	p1 := netip.MustParseAddr("192.0.2.1")
	p2 := netip.MustParseAddr("192.0.2.2")
	pfx := netip.MustParsePrefix("10.1.0.0/24")

	ribRef.Update(
		rib.Route{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.10"), Peer: p1, SourceID: rib.RouteSourceBird, Pref: 200},
		rib.Route{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.20"), Peer: p2, SourceID: rib.RouteSourceBird, Pref: 100},
	)

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)

	// Collect is_best flags by nexthop address string for deterministic assertions.
	bestByNexthop := map[string]bool{}
	for _, r := range resp.Routes {
		addr, addrErr := r.GetNextHop().ToAddr()
		require.NoError(t, addrErr)
		bestByNexthop[addr.String()] = r.GetIsBest()
	}

	require.True(t, bestByNexthop["10.0.0.10"], "higher-Pref bird route must be best")
	require.False(t, bestByNexthop["10.0.0.20"], "lower-Pref bird route must not be best")
	require.True(t, bestByNexthop["10.0.0.2"], "static route must be best within its source")
}

func TestInsertRoute_NonStaticMultipleNexthops_InvalidArgument(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	req := &operatorpb.InsertRouteRequest{
		Name:   "route0",
		Prefix: "10.0.0.0/24",
		NexthopAddrs: []*commonpb.IPAddress{
			commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1")),
			commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.2")),
		},
		SourceId: operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
	}

	_, err := svc.InsertRoute(context.Background(), req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}
