package mpls_test

import (
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

// weightedRoute returns an MPLS route to a fixed prefix, address and label
// under the given RD, carrying the given weight.
func weightedRoute(rd uint64, weight uint32) rib.Route {
	route := mplsRoute("2001:db8:1::/48", "2001:db8::1", 100)
	route.RD = rd
	route.LargeCommunities = []rib.LargeCommunity{{ASN: 13238, Function: 1, Value: weight}}
	return route
}
