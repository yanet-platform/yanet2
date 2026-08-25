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

// mustNetwork builds the wire prefix message from a CIDR string.
func mustNetwork(t *testing.T, s string) *commonpb.IPPrefix {
	t.Helper()

	network, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix(s))
	require.NoError(t, err)

	return network
}

// TestShowRoutes_StaticECMP_BothBest verifies that two static ECMP nexthops
// for the same prefix both carry is_best=true.
func TestShowRoutes_StaticECMP_BothBest(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	nh1 := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))
	nh2 := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.2"))

	_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
		Name:         "route0",
		Prefix:       mustNetwork(t, "10.0.0.0/24"),
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
		Prefix:       mustNetwork(t, "10.1.0.0/24"),
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

// TestShowRoutes_UnknownConfig_NotFound verifies that ShowRoutes reports
// NotFound for a config name that was never registered, distinguishing it
// from a registered config that genuinely holds no routes.
func TestShowRoutes_UnknownConfig_NotFound(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	_, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestShowRoutes_EmptyConfig_Success verifies that a registered config with
// no routes still returns a normal empty success.
func TestShowRoutes_EmptyConfig_Success(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())
	svc.getOrCreateRib("route0")

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Empty(t, resp.GetRoutes())
}

// TestLookupRoute_ThreeWay verifies that LookupRoute distinguishes an
// unknown config (NotFound) from a registered config with no matching route
// (empty success) and from a registered config with a match (the route).
func TestLookupRoute_ThreeWay(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	addr := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))

	t.Run("unknown config", func(t *testing.T) {
		_, err := svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "missing", IpAddr: addr})
		require.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("no matching route", func(t *testing.T) {
		svc.getOrCreateRib("route0")

		resp, err := svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "route0", IpAddr: addr})
		require.NoError(t, err)
		require.Empty(t, resp.GetRoutes())
	})

	t.Run("matching route", func(t *testing.T) {
		_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
			Name:         "route0",
			Prefix:       mustNetwork(t, "10.0.0.0/24"),
			NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))},
			SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
		})
		require.NoError(t, err)

		resp, err := svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "route0", IpAddr: addr})
		require.NoError(t, err)
		matched, err := resp.GetPrefix().ToPrefix()
		require.NoError(t, err)
		require.Equal(t, netip.MustParsePrefix("10.0.0.0/24"), matched)
		require.Len(t, resp.GetRoutes(), 1)
	})
}

func TestInsertRoute_NonStaticMultipleNexthops_InvalidArgument(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	req := &operatorpb.InsertRouteRequest{
		Name:   "route0",
		Prefix: mustNetwork(t, "10.0.0.0/24"),
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

// TestInsertRoute_MalformedPrefix_InvalidArgument verifies that a prefix
// the shared type cannot decode never reaches the RIB.
//
// Every malformed shape stays on the InvalidArgument path, and a rejected
// insert leaves no RIB behind for the named config.
func TestInsertRoute_MalformedPrefix_InvalidArgument(t *testing.T) {
	nexthops := []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))}

	testCases := []struct {
		name   string
		prefix *commonpb.IPPrefix
	}{
		{
			name:   "missing prefix",
			prefix: nil,
		},
		{
			name:   "unset oneof",
			prefix: &commonpb.IPPrefix{},
		},
		{
			name: "missing addr",
			prefix: &commonpb.IPPrefix{
				Prefix: &commonpb.IPPrefix_V4{V4: &commonpb.IPv4Prefix{PrefixLen: 24}},
			},
		},
		{
			name: "prefix length beyond address family",
			prefix: &commonpb.IPPrefix{
				Prefix: &commonpb.IPPrefix_V4{V4: &commonpb.IPv4Prefix{Addr: &commonpb.IPv4Address{Addr: 0x0a000000}, PrefixLen: 33}},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			svc := NewRouteService(neigh.NewNeighTable())

			_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
				Name:         "route0",
				Prefix:       testCase.prefix,
				NexthopAddrs: nexthops,
				SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))

			_, ok := svc.ribs.Get("route0")
			require.False(t, ok, "a rejected insert must not create a RIB")
		})
	}
}

// TestInsertRoute_HostBitsAreMasked verifies that host bits below the
// prefix length are masked off before the route reaches the RIB.
//
// The route is therefore stored, and reported back, under its network
// address rather than under the address the caller sent.
func TestInsertRoute_HostBitsAreMasked(t *testing.T) {
	svc := NewRouteService(neigh.NewNeighTable())

	_, err := svc.InsertRoute(t.Context(), &operatorpb.InsertRouteRequest{
		Name: "route0",
		// 10.0.0.7/24 with host bits deliberately left set.
		Prefix: &commonpb.IPPrefix{
			Prefix: &commonpb.IPPrefix_V4{V4: &commonpb.IPv4Prefix{Addr: &commonpb.IPv4Address{Addr: 0x0a000007}, PrefixLen: 24}},
		},
		NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.168.1.1"))},
		SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
	})
	require.NoError(t, err)

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Len(t, resp.GetRoutes(), 1)

	prefix, err := resp.GetRoutes()[0].GetPrefix().ToPrefix()
	require.NoError(t, err)
	require.Equal(t, netip.MustParsePrefix("10.0.0.0/24"), prefix)
}

// TestShowRoutes_ConfiguredModule_NoRIBYet_Success verifies that ShowRoutes
// returns an empty success for a module the operator declares as its own
// even before any RIB exists for it, and that answering the read does not
// create a RIB or wake the reconcile loop.
func TestShowRoutes_ConfiguredModule_NoRIBYet_Success(t *testing.T) {
	var onChangedCount int
	svc := NewRouteService(
		neigh.NewNeighTable(),
		WithRouteServiceConfiguredModules("route0"),
		WithRouteServiceOnChanged(func() { onChangedCount++ }),
	)

	resp, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "route0"})
	require.NoError(t, err)
	require.Empty(t, resp.GetRoutes())

	_, ok := svc.ribs.Get("route0")
	require.False(t, ok, "answering a read for a configured module must not create a RIB")
	require.Zero(t, onChangedCount, "answering a read must not wake the reconcile loop")
}

// TestLookupRoute_ConfiguredModule_NoRIBYet_Success verifies that
// LookupRoute returns an empty success for a module the operator declares
// as its own even before any RIB exists for it, and that answering the
// read does not create a RIB or wake the reconcile loop.
func TestLookupRoute_ConfiguredModule_NoRIBYet_Success(t *testing.T) {
	var onChangedCount int
	svc := NewRouteService(
		neigh.NewNeighTable(),
		WithRouteServiceConfiguredModules("route0"),
		WithRouteServiceOnChanged(func() { onChangedCount++ }),
	)

	addr := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))
	resp, err := svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "route0", IpAddr: addr})
	require.NoError(t, err)
	require.Empty(t, resp.GetRoutes())

	_, ok := svc.ribs.Get("route0")
	require.False(t, ok, "answering a read for a configured module must not create a RIB")
	require.Zero(t, onChangedCount, "answering a read must not wake the reconcile loop")
}

// TestShowRoutesAndLookupRoute_UndeclaredConfig_NotFound verifies that a
// name outside the configured-modules set stays NotFound even when the
// set is non-empty, distinguishing it from a declared-but-unpopulated name.
func TestShowRoutesAndLookupRoute_UndeclaredConfig_NotFound(t *testing.T) {
	svc := NewRouteService(
		neigh.NewNeighTable(),
		WithRouteServiceConfiguredModules("route0"),
	)

	_, err := svc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: "other"})
	require.Equal(t, codes.NotFound, status.Code(err))

	addr := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))
	_, err = svc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: "other", IpAddr: addr})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestNewOperator_ConfiguredModuleNoRIBYet_ReadsSucceedWithoutCreatingRIB
// verifies that an Operator built by NewOperator answers ShowRoutes and
// LookupRoute for its configured module with an empty success before any
// RIB exists for it, and that neither read creates one.
func TestNewOperator_ConfiguredModuleNoRIBYet_ReadsSucceedWithoutCreatingRIB(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NetlinkMonitor.Disabled = true

	op, err := NewOperator(cfg)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, op.Close())
	}()

	moduleName := cfg.Function.Module.Unwrap()

	showResp, err := op.routeSvc.ShowRoutes(t.Context(), &operatorpb.ShowRoutesRequest{Name: moduleName})
	require.NoError(t, err)
	require.Empty(t, showResp.GetRoutes())

	addr := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("10.0.0.1"))
	lookupResp, err := op.routeSvc.LookupRoute(t.Context(), &operatorpb.LookupRouteRequest{Name: moduleName, IpAddr: addr})
	require.NoError(t, err)
	require.Empty(t, lookupResp.GetRoutes())

	_, ok := op.routeSvc.ribs.Get(moduleName)
	require.False(t, ok, "answering reads for the configured module must not create a RIB")
}

// TestListConfigs_ConfiguredModule_ReportedOnceBeforeAndAfterRIB verifies
// that a configured module name appears in ListConfigs before its RIB is
// created, still appears exactly once after the RIB is created, and that
// the full result is always lexicographically sorted regardless of the
// order configs and RIBs were added in.
func TestListConfigs_ConfiguredModule_ReportedOnceBeforeAndAfterRIB(t *testing.T) {
	svc := NewRouteService(
		neigh.NewNeighTable(),
		WithRouteServiceConfiguredModules("route0"),
	)

	resp, err := svc.ListConfigs(t.Context(), &operatorpb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"route0"}, resp.GetConfigs())

	svc.getOrCreateRib("route0")
	svc.getOrCreateRib("route2")
	svc.getOrCreateRib("route1")

	resp, err = svc.ListConfigs(t.Context(), &operatorpb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"route0", "route1", "route2"}, resp.GetConfigs())
}
