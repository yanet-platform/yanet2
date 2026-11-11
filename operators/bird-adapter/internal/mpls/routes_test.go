package mpls_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/mpls"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
)

// Test_Rib_Apply_LabelChange verifies that a label change withdraws the
// nexthop under the old label and adds it under the new one.
func Test_Rib_Apply_LabelChange(t *testing.T) {
	mplsRib := mpls.NewRib()

	mplsRib.Apply(mplsRoute("2001:db8:1::/48", "2001:db8::1", 100))
	changes := mplsRib.Apply(mplsRoute("2001:db8:1::/48", "2001:db8::1", 200))

	require.Len(t, changes, 2)
	require.True(t, changes[0].ToRemove)
	require.Equal(t, []uint32{100}, changes[0].MplsLabelStack)
	require.False(t, changes[1].ToRemove)
	require.Equal(t, []uint32{200}, changes[1].MplsLabelStack)
}

// Test_Rib_Apply_SharedNexthopWeightChange verifies that once two routes
// sharing a nexthop agree on a weight, that weight is the last one sent.
func Test_Rib_Apply_SharedNexthopWeightChange(t *testing.T) {
	mplsRib := mpls.NewRib()

	mplsRib.Apply(weightedRoute(1, 1))
	mplsRib.Apply(weightedRoute(2, 2))
	changes := mplsRib.Apply(weightedRoute(2, 1))

	require.NotEmpty(t, changes)
	last := changes[len(changes)-1]
	require.False(t, last.ToRemove)
	require.Equal(t, []rib.LargeCommunity{{ASN: 13238, Function: 1, Value: 1}}, last.LargeCommunities)
}

// Test_Rib_Apply_BestPeerWithdraw verifies that when the preferred peer
// withdraws a route another peer still announces, that peer's route stays.
func Test_Rib_Apply_BestPeerWithdraw(t *testing.T) {
	mplsRib := mpls.NewRib()
	preferred := peerRoute("198.51.100.1", 200, 1)
	backup := peerRoute("198.51.100.2", 100, 1)

	mplsRib.Apply(preferred)
	mplsRib.Apply(backup)
	preferred.ToRemove = true
	changes := mplsRib.Apply(preferred)

	require.Empty(t, changes, "the nexthop stays while the backup peer announces it")
	require.Equal(t, []rib.Route{backup}, mplsRib.Best())
}

// Test_Rib_Apply_BestPeerDemotion verifies that when an update ranks the
// preferred peer below another one, that peer's route replaces it.
func Test_Rib_Apply_BestPeerDemotion(t *testing.T) {
	mplsRib := mpls.NewRib()
	preferred := peerRoute("198.51.100.1", 200, 1)
	backup := peerRoute("198.51.100.2", 100, 2)

	mplsRib.Apply(preferred)
	mplsRib.Apply(backup)
	preferred.Pref = 50
	changes := mplsRib.Apply(preferred)

	require.Equal(t, []rib.Route{backup}, changes, "the backup peer's weight reaches route-mpls")
	require.Equal(t, []rib.Route{backup}, mplsRib.Best())
}

// Test_Rib_Apply_TiedPeersKeepBest verifies that the best of many equally
// preferred peers stays put when another of them changes.
func Test_Rib_Apply_TiedPeersKeepBest(t *testing.T) {
	mplsRib := mpls.NewRib()
	// More than twelve, past which an unstable sort reorders equal routes.
	peers := make([]rib.Route, 13)
	for idx := range peers {
		peers[idx] = peerRoute(fmt.Sprintf("198.51.100.%d", idx+1), 100, uint32(idx+1))
		mplsRib.Apply(peers[idx])
	}

	peers[1].Pref = 50
	changes := mplsRib.Apply(peers[1])

	require.Empty(t, changes)
	require.Equal(t, []rib.Route{peers[0]}, mplsRib.Best())
}

// weightedRoute returns an MPLS route to a fixed prefix, address and label
// under the given RD, carrying the given weight.
func weightedRoute(rd uint64, weight uint32) rib.Route {
	route := mplsRoute("2001:db8:1::/48", "2001:db8::1", 100)
	route.RD = rd
	route.LargeCommunities = []rib.LargeCommunity{{ASN: 13238, Function: 1, Value: weight}}
	return route
}

// peerRoute returns an MPLS route to a fixed prefix, address and label under
// RD 1, as the given peer announces it at the given preference and weight.
func peerRoute(peer string, pref uint32, weight uint32) rib.Route {
	route := weightedRoute(1, weight)
	route.Peer = netip.MustParseAddr(peer)
	route.Pref = pref
	return route
}
