package mpls_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/mpls"
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
