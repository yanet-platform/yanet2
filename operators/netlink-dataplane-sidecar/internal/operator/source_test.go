package operator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
)

// Test_Source_ImmutableSnapshot verifies that caller mutations cannot change
// startup configuration and wake bursts remain bounded and nonblocking.
func Test_Source_ImmutableSnapshot(t *testing.T) {
	state, err := netplan.Parse([]byte("network: {version: 2, ethernets: {kni0: {addresses: ['fe80::1/64'], accept-ra: false}}}"))
	require.NoError(t, err)
	expected := state.Clone()
	source := sidecaroperator.NewSource(state)
	state.Links[0].Name = "changed"
	*state.Links[0].AcceptRA = true
	state.Links[0].Addresses = nil
	for range 1000 {
		source.Notify()
	}
	<-source.Wake()
	require.Empty(t, source.Wake())
	snapshot, ok := source.Snapshot()
	require.True(t, ok)
	require.Equal(t, expected, snapshot)
	snapshot.Links[0].LinkLocal[0] = "changed"
	source.Advance(snapshot)
	snapshot, ok = source.Snapshot()
	require.True(t, ok)
	require.Equal(t, expected, snapshot)
	empty, ok := sidecaroperator.NewSource(sidecaroperator.State{}).Snapshot()
	require.True(t, ok)
	require.Empty(t, empty.Links)
}
