package route_test

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

// Test_Store_ReplaceIsAtomicAndSnapshotsAreIndependent verifies that readers
// observe only complete replacements and cannot mutate stored state.
func Test_Store_ReplaceIsAtomicAndSnapshotsAreIndependent(t *testing.T) {
	first := []route.Route{
		testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
		testRoute("2001:db8::/64", "2001:db8::1", "kni1"),
	}
	second := []route.Route{
		testRoute("198.51.100.0/24", "198.51.100.1", "kni2"),
	}
	store := route.NewStore()
	startup, initialized := store.SnapshotState()
	require.Empty(t, startup)
	require.False(t, initialized)

	input := append([]route.Route(nil), first...)
	require.NoError(t, store.Replace(input))
	input[0] = second[0]
	committed, initialized := store.SnapshotState()
	require.True(t, initialized)
	require.Equal(t, first, committed)

	snapshot, initialized := store.SnapshotState()
	require.True(t, initialized)
	snapshot[0] = second[0]
	require.Equal(t, first, store.Snapshot())

	writerError := make(chan error, 1)
	go func() {
		for idx := range 2_000 {
			candidate := first
			if idx%2 != 0 {
				candidate = second
			}
			if err := store.Replace(candidate); err != nil {
				writerError <- err
				return
			}
		}
		writerError <- nil
	}()

	for range 2_000 {
		current, currentInitialized := store.SnapshotState()
		require.True(t, currentInitialized)
		require.True(
			t,
			reflect.DeepEqual(current, first) || reflect.DeepEqual(current, second),
			"snapshot must equal one complete replacement: %#v",
			current,
		)
	}
	require.NoError(t, <-writerError)
}

// Test_Store_SnapshotStateDistinguishesInitializedEmpty verifies that one
// observation distinguishes startup from a committed empty snapshot.
func Test_Store_SnapshotStateDistinguishesInitializedEmpty(t *testing.T) {
	store := route.NewStore()

	snapshot, initialized := store.SnapshotState()
	require.Empty(t, snapshot)
	require.False(t, initialized)

	require.NoError(t, store.Replace(nil))
	snapshot, initialized = store.SnapshotState()
	require.Empty(t, snapshot)
	require.True(t, initialized)
}

func Test_Store_FailedUpdateRestoresPreviousSnapshot(t *testing.T) {
	previous := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	candidate := []route.Route{testRoute("198.51.100.0/24", "198.51.100.1", "kni1")}
	store := route.NewStore()
	require.NoError(t, store.Replace(previous))
	require.True(t, wakeReady(store.Wake()))

	update, err := store.ReplaceTracked(candidate)
	require.NoError(t, err)
	require.True(t, wakeReady(store.Wake()))
	applyErr := errors.New("kernel apply failed")
	update.Complete(applyErr)

	require.ErrorIs(t, update.Wait(t.Context()), applyErr)
	snapshot, initialized := store.SnapshotState()
	require.True(t, initialized)
	require.Equal(t, previous, snapshot)
	require.True(t, wakeReady(store.Wake()))
}

func Test_Store_FailedSupersedingUpdateRestoresLastSuccessfulSnapshot(t *testing.T) {
	stable := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	firstRejected := []route.Route{testRoute("198.51.100.0/24", "198.51.100.1", "kni1")}
	secondRejected := []route.Route{testRoute("203.0.113.0/24", "203.0.113.1", "kni2")}
	store := route.NewStore()
	require.NoError(t, store.Replace(stable))
	require.True(t, wakeReady(store.Wake()))

	firstUpdate, err := store.ReplaceTracked(firstRejected)
	require.NoError(t, err)
	require.True(t, wakeReady(store.Wake()))
	secondUpdate, err := store.ReplaceTracked(secondRejected)
	require.NoError(t, err)
	require.True(t, wakeReady(store.Wake()))

	firstErr := errors.New("first snapshot failed after being superseded")
	firstUpdate.Complete(firstErr)
	require.ErrorIs(t, firstUpdate.Wait(t.Context()), firstErr)
	require.Equal(t, secondRejected, store.Snapshot())

	secondErr := errors.New("second snapshot failed")
	secondUpdate.Complete(secondErr)
	require.ErrorIs(t, secondUpdate.Wait(t.Context()), secondErr)
	require.Equal(t, stable, store.Snapshot())
	require.True(t, wakeReady(store.Wake()))
}

func Test_Store_StaleSuccessCannotReplaceNewerFailureRollbackBaseline(t *testing.T) {
	stable := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	older := []route.Route{testRoute("198.51.100.0/24", "198.51.100.1", "kni1")}
	newer := []route.Route{testRoute("203.0.113.0/24", "203.0.113.1", "kni2")}
	store := route.NewStore()
	require.NoError(t, store.Replace(stable))
	require.True(t, wakeReady(store.Wake()))

	olderUpdate, err := store.ReplaceTracked(older)
	require.NoError(t, err)
	newerUpdate, err := store.ReplaceTracked(newer)
	require.NoError(t, err)
	require.True(t, wakeReady(store.Wake()))
	newerUpdate.Complete(errors.New("newer snapshot failed"))
	require.Equal(t, stable, store.Snapshot())
	require.True(t, wakeReady(store.Wake()))

	olderUpdate.Complete(nil)
	require.Equal(t, stable, store.Snapshot())
	require.True(t, wakeReady(store.Wake()))

	lastUpdate, err := store.ReplaceTracked(newer)
	require.NoError(t, err)
	require.True(t, wakeReady(store.Wake()))
	lastUpdate.Complete(errors.New("last snapshot failed"))
	require.Equal(t, stable, store.Snapshot())
}

func Test_Store_StaleFailureWakesReconciliationOfNewerSnapshot(t *testing.T) {
	older := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	newer := []route.Route{testRoute("198.51.100.0/24", "198.51.100.1", "kni1")}
	store := route.NewStore()
	olderUpdate, err := store.ReplaceTracked(older)
	require.NoError(t, err)
	newerUpdate, err := store.ReplaceTracked(newer)
	require.NoError(t, err)
	require.True(t, wakeReady(store.Wake()))

	newerUpdate.Complete(nil)
	olderUpdate.Complete(errors.New("stale apply rolled back"))

	require.Equal(t, newer, store.Snapshot())
	require.True(t, wakeReady(store.Wake()))
}

func Test_Update_WaitPrefersCompletedResultOverCanceledContext(t *testing.T) {
	store := route.NewStore()
	update, err := store.ReplaceTracked([]route.Route{
		testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
	})
	require.NoError(t, err)
	applyErr := errors.New("kernel apply failed")
	update.Complete(applyErr)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, update.Wait(ctx), applyErr)
}

// Test_Store_WakeCoalescesSuccessfulReplacements verifies that pending wake
// signals occupy one buffered slot and failed updates never signal.
func Test_Store_WakeCoalescesSuccessfulReplacements(t *testing.T) {
	store := route.NewStore()
	_, initialized := store.SnapshotState()
	require.False(t, initialized)
	require.False(t, wakeReady(store.Wake()))

	require.NoError(t, store.Replace([]route.Route{
		testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
	}))
	_, initialized = store.SnapshotState()
	require.True(t, initialized)
	require.NoError(t, store.Replace([]route.Route{
		testRoute("198.51.100.0/24", "198.51.100.1", "kni1"),
	}))
	require.True(t, wakeReady(store.Wake()))
	require.False(t, wakeReady(store.Wake()))

	err := store.Replace([]route.Route{
		testRoute("203.0.113.1/24", "203.0.113.2", "kni2"),
	})
	require.ErrorContains(t, err, "is not masked")
	require.False(t, wakeReady(store.Wake()))
	store.Notify()
	store.Notify()
	require.True(t, wakeReady(store.Wake()))
	require.False(t, wakeReady(store.Wake()))
}

// Test_Store_RejectsInvalidSnapshots verifies that every route invariant and
// exact duplicate detection protect the prior complete snapshot.
func Test_Store_RejectsInvalidSnapshots(t *testing.T) {
	valid := testRoute("192.0.2.0/24", "192.0.2.1", "kni0")
	tests := []struct {
		name          string
		routes        []route.Route
		errorContains string
	}{
		{
			name: "invalid prefix",
			routes: []route.Route{{
				Nexthop:   netip.MustParseAddr("192.0.2.1"),
				Interface: "kni0",
			}},
			errorContains: "prefix",
		},
		{
			name: "unmasked prefix",
			routes: []route.Route{{
				Prefix:    netip.MustParsePrefix("192.0.2.1/24"),
				Nexthop:   netip.MustParseAddr("192.0.2.2"),
				Interface: "kni0",
			}},
			errorContains: "is not masked",
		},
		{
			name: "invalid nexthop",
			routes: []route.Route{{
				Prefix:    netip.MustParsePrefix("192.0.2.0/24"),
				Interface: "kni0",
			}},
			errorContains: "nexthop",
		},
		{
			name: "zoned nexthop",
			routes: []route.Route{{
				Prefix:    netip.MustParsePrefix("fe80::/64"),
				Nexthop:   netip.MustParseAddr("fe80::1%kni0"),
				Interface: "kni0",
			}},
			errorContains: "has a zone",
		},
		{
			name: "empty interface",
			routes: []route.Route{{
				Prefix:  netip.MustParsePrefix("192.0.2.0/24"),
				Nexthop: netip.MustParseAddr("192.0.2.1"),
			}},
			errorContains: "interface is empty",
		},
		{
			name: "interface too long",
			routes: []route.Route{testRoute(
				"192.0.2.0/24",
				"192.0.2.1",
				"interface-name-too-long",
			)},
			errorContains: "exceeds Linux IFNAMSIZ",
		},
		{
			name: "family mismatch",
			routes: []route.Route{{
				Prefix:    netip.MustParsePrefix("192.0.2.0/24"),
				Nexthop:   netip.MustParseAddr("2001:db8::1"),
				Interface: "kni0",
			}},
			errorContains: "different address families",
		},
		{
			name: "IPv4-mapped prefix",
			routes: []route.Route{testRoute(
				"::ffff:192.0.2.0/120",
				"::ffff:192.0.2.1",
				"kni0",
			)},
			errorContains: "IPv4-mapped IPv6 prefix",
		},
		{
			name: "IPv4-mapped nexthop",
			routes: []route.Route{testRoute(
				"2001:db8::/64",
				"::ffff:192.0.2.1",
				"kni0",
			)},
			errorContains: "IPv4-mapped IPv6 address",
		},
		{
			name:          "exact duplicate",
			routes:        []route.Route{valid, valid},
			errorContains: "duplicates route 0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := route.NewStore()
			require.NoError(t, store.Replace([]route.Route{valid}))
			require.True(t, wakeReady(store.Wake()))

			err := store.Replace(test.routes)
			require.ErrorContains(t, err, test.errorContains)
			require.Equal(t, []route.Route{valid}, store.Snapshot())
			require.False(t, wakeReady(store.Wake()))
		})
	}
}

// Test_Store_ReplaceAllowsECMP verifies that equal prefixes remain valid when
// either the nexthop or output interface distinguishes each path.
func Test_Store_ReplaceAllowsECMP(t *testing.T) {
	routes := []route.Route{
		testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
		testRoute("192.0.2.0/24", "192.0.2.2", "kni0"),
		testRoute("192.0.2.0/24", "192.0.2.1", "kni1"),
	}
	store := route.NewStore()

	require.NoError(t, store.Replace(routes))
	require.Equal(t, routes, store.Snapshot())
}

func Test_Store_RejectsOversizedECMPBeforeCommit(t *testing.T) {
	store := route.NewStore()
	routes := make([]route.Route, 4096)
	for idx := range routes {
		routes[idx] = route.Route{
			Prefix: netip.MustParsePrefix("192.0.2.0/24"),
			Nexthop: netip.AddrFrom4([4]byte{
				10,
				byte(idx >> 16),
				byte(idx >> 8),
				byte(idx),
			}),
			Interface: "kni0",
		}
	}

	err := store.Replace(routes)
	require.ErrorContains(t, err, "exceed the Linux multipath attribute limit")
	snapshot, initialized := store.SnapshotState()
	require.False(t, initialized)
	require.Empty(t, snapshot)
}

// testRoute returns one fully valid static route fixture.
func testRoute(prefix, nexthop, interfaceName string) route.Route {
	return route.Route{
		Prefix:    netip.MustParsePrefix(prefix),
		Nexthop:   netip.MustParseAddr(nexthop),
		Interface: interfaceName,
	}
}

// wakeReady consumes one pending signal and reports whether one was available.
func wakeReady(wake <-chan struct{}) bool {
	select {
	case <-wake:
		return true
	default:
		return false
	}
}
