package rib

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newTestRIB constructs a RIB suitable for unit tests.
func newTestRIB(t *testing.T) *RIB {
	t.Helper()
	return NewRIB(zap.NewNop())
}

// routesForPrefix returns the routes stored for the given prefix, or nil if the
// prefix is absent.
func routesForPrefix(t *testing.T, r *RIB, prefix netip.Prefix) []Route {
	t.Helper()
	dump := r.DumpRoutes()
	for _, level := range dump {
		for k, v := range level {
			if k == prefix {
				return v.Routes
			}
		}
	}
	return nil
}

// TestRemoveUnicastRoute_BGPPeerIsolation verifies that calling RemoveUnicastRoute
// with a Bird source does not remove BGP routes from distinct peers even when
// the nexthop matches.
//
// BGP route identity is peer-scoped; the unary API carries no peer, so the
// candidate Peer is IPv6Unspecified. A real BGP route has a non-unspecified
// Peer, so isSameIdentity must return false and neither route may be removed.
func TestRemoveUnicastRoute_BGPPeerIsolation(t *testing.T) {
	pfx := netip.MustParsePrefix("10.0.0.0/24")
	nh := netip.MustParseAddr("192.0.2.1")
	peer1 := netip.MustParseAddr("10.1.1.1")
	peer2 := netip.MustParseAddr("10.1.1.2")

	r := newTestRIB(t)

	r.Update(
		Route{Prefix: pfx, NextHop: nh, Peer: peer1, SourceID: RouteSourceBird},
		Route{Prefix: pfx, NextHop: nh, Peer: peer2, SourceID: RouteSourceBird},
	)

	routes := routesForPrefix(t, r, pfx)
	require.Len(t, routes, 2, "setup: both BGP routes must be present before removal attempt")

	err := r.RemoveUnicastRoute(pfx, nh, RouteSourceBird)
	require.NoError(t, err)

	routes = routesForPrefix(t, r, pfx)
	require.Len(t, routes, 2,
		"RemoveUnicastRoute must not remove BGP routes from distinct peers")
}

// TestRemoveUnicastRoute_StaticECMP verifies that removing one nexthop from a
// static ECMP set leaves the remaining nexthop intact.
func TestRemoveUnicastRoute_StaticECMP(t *testing.T) {
	pfx := netip.MustParsePrefix("10.0.0.0/24")
	nh1 := netip.MustParseAddr("192.0.2.1")
	nh2 := netip.MustParseAddr("192.0.2.2")

	r := newTestRIB(t)

	require.NoError(t, r.AddUnicastRoute(pfx, nh1, RouteSourceStatic))
	require.NoError(t, r.AddUnicastRoute(pfx, nh2, RouteSourceStatic))

	routes := routesForPrefix(t, r, pfx)
	require.Len(t, routes, 2, "setup: both static nexthops must be present")

	require.NoError(t, r.RemoveUnicastRoute(pfx, nh1, RouteSourceStatic))

	routes = routesForPrefix(t, r, pfx)
	require.Len(t, routes, 1, "exactly one nexthop must remain after removing the other")
	require.Equal(t, nh2, routes[0].NextHop)
}

// TestRemoveUnicastRoute_StaticSingle verifies that removing the sole static
// nexthop for a prefix deletes the prefix entry entirely.
func TestRemoveUnicastRoute_StaticSingle(t *testing.T) {
	pfx := netip.MustParsePrefix("10.0.0.0/24")
	nh := netip.MustParseAddr("192.0.2.1")

	r := newTestRIB(t)

	require.NoError(t, r.AddUnicastRoute(pfx, nh, RouteSourceStatic))

	routes := routesForPrefix(t, r, pfx)
	require.Len(t, routes, 1, "setup: static route must be present")

	require.NoError(t, r.RemoveUnicastRoute(pfx, nh, RouteSourceStatic))

	routes = routesForPrefix(t, r, pfx)
	require.Nil(t, routes, "prefix entry must be absent after removing the sole nexthop")
}
