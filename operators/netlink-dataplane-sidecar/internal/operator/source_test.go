package operator_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
)

// sourceFixture provides addresses and optional policy requiring deep copies.
func sourceFixture(t *testing.T) desired.State {
	t.Helper()
	state, err := netplan.Parse([]byte("network: {version: 2, ethernets: {kni0: {addresses: ['fe80::1/64'], accept-ra: false}}}"))
	require.NoError(t, err)
	return state
}

// Test_Source_InputIsolation verifies that constructor input can be changed
// without changing the configuration subsequently exposed to reconciliation.
func Test_Source_InputIsolation(t *testing.T) {
	state := sourceFixture(t)
	expected := state.Clone()
	source := sidecaroperator.NewSource(state)
	state.Links[0].Name = "changed"
	*state.Links[0].AcceptRA = true
	state.Links[0].Addresses[0] = netip.MustParsePrefix("192.0.2.1/24")
	snapshot, ok := source.Snapshot()
	require.True(t, ok)
	require.Equal(t, expected, snapshot)
}

// Test_Source_SnapshotIsolation verifies that mutating a returned snapshot
// cannot change the next desired-state capture.
func Test_Source_SnapshotIsolation(t *testing.T) {
	state := sourceFixture(t)
	source := sidecaroperator.NewSource(state)
	snapshot, ok := source.Snapshot()
	require.True(t, ok)
	snapshot.Links[0].IPv6LinkLocal = false
	*snapshot.Links[0].AcceptRA = true
	snapshot.Links[0].Addresses[0] = netip.MustParsePrefix("192.0.2.1/24")
	snapshot, ok = source.Snapshot()
	require.True(t, ok)
	require.Equal(t, state, snapshot)
}

// Test_Source_AdvanceRetainsDesiredState verifies that acknowledging an apply
// cannot replace the immutable startup configuration.
func Test_Source_AdvanceRetainsDesiredState(t *testing.T) {
	state := sourceFixture(t)
	source := sidecaroperator.NewSource(state)
	source.Advance(desired.State{})
	require.Empty(t, source.Wake())
	snapshot, ok := source.Snapshot()
	require.True(t, ok)
	require.Equal(t, state, snapshot)
}

// Test_Source_NotifyCoalesces verifies that event bursts retain one pending
// collection without blocking reception or changing the immutable topology.
func Test_Source_NotifyCoalesces(t *testing.T) {
	state := sourceFixture(t)
	source := sidecaroperator.NewSource(state)
	for range 1000 {
		source.Notify()
	}
	require.Len(t, source.Wake(), 1)
	<-source.Wake()
	snapshot, ok := source.Snapshot()
	require.True(t, ok)
	require.Equal(t, state, snapshot)
}

// Test_Source_EmptySnapshot verifies that an explicitly empty configuration
// remains available as authoritative desired state.
func Test_Source_EmptySnapshot(t *testing.T) {
	empty, ok := sidecaroperator.NewSource(sidecaroperator.State{}).Snapshot()
	require.True(t, ok)
	require.Empty(t, empty.Links)
}
