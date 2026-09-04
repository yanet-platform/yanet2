package operator_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

func TestSourceDistinguishesStartupFromExplicitEmptySnapshot(t *testing.T) {
	store := route.NewStore()
	source := sidecaroperator.NewSource(store)

	startup, ok := source.Snapshot()
	require.True(t, ok)
	require.False(t, startup.Initialized)
	require.Empty(t, startup.Routes)

	require.NoError(t, store.Replace(nil))
	committed, ok := source.Snapshot()
	require.True(t, ok)
	require.True(t, committed.Initialized)
	require.Empty(t, committed.Routes)
}

func TestSourceReturnsCurrentRoutesAndStoreWake(t *testing.T) {
	store := route.NewStore()
	source := sidecaroperator.NewSource(store)
	routes := []route.Route{{
		Prefix:    netip.MustParsePrefix("192.0.2.0/24"),
		Nexthop:   netip.MustParseAddr("192.0.2.1"),
		Interface: "kni0",
	}}

	require.NoError(t, store.Replace(routes))
	select {
	case <-source.Wake():
	default:
		t.Fatal("route replacement did not wake source")
	}

	snapshot, ok := source.Snapshot()
	require.True(t, ok)
	require.True(t, snapshot.Initialized)
	require.Equal(t, routes, snapshot.Routes)

	snapshot.Routes[0].Interface = "changed"
	current, _ := source.Snapshot()
	require.Equal(t, "kni0", current.Routes[0].Interface)

	store.Notify()
	select {
	case <-source.Wake():
	default:
		t.Fatal("store notification did not wake source")
	}
}
