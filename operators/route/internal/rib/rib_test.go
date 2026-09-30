package rib

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// newTestRIB constructs a RIB suitable for unit tests.
func newTestRIB(t *testing.T) *RIB {
	t.Helper()
	return NewRIB()
}

// birdRoute builds a BGP path identified by the given peer and global ID, as
// BIRD identity is the peer and global ID pair rather than the nexthop.
func birdRoute(prefix netip.Prefix, peer string, globalID uint32, sessionID uint64) Route {
	return Route{
		SessionID: sessionID,
		Prefix:    prefix,
		NextHop:   netip.MustParseAddr("2001:db8:ff::1"),
		Peer:      netip.MustParseAddr(peer),
		GlobalID:  globalID,
		SourceID:  RouteSourceBird,
		UpdatedAt: time.Now(),
	}
}

// peersForPrefix returns the peers of the routes stored for the given prefix,
// in RIB order.
func peersForPrefix(t *testing.T, r *RIB, prefix netip.Prefix) []netip.Addr {
	t.Helper()
	var peers []netip.Addr
	for _, route := range routesForPrefix(t, r, prefix) {
		peers = append(peers, route.Peer)
	}
	return peers
}

// countDump returns the prefix and route totals actually stored in the RIB,
// asserting on the way that every stored key is a usable prefix.
func countDump(t *testing.T, r *RIB) (prefixes int, routes int) {
	t.Helper()
	for prefix, list := range r.DumpRoutes().Dump() {
		require.True(t, prefix.IsValid(), "RIB must not store an invalid prefix")
		prefixes++
		routes += len(list.Routes)
	}
	return prefixes, routes
}

// runCleanup runs a cleanup pass for the given session with no TTL delay.
func runCleanup(t *testing.T, r *RIB, sessionID uint64) {
	t.Helper()
	r.CleanupTask(sessionID, make(chan bool), 0)
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
// BGP route identity is peer-scoped. The unary API carries no peer, so the
// candidate Peer is IPv6Unspecified. A real BGP route has a non-unspecified
// Peer, so IsSameIdentity must return false and neither route may be removed.
func TestRemoveUnicastRoute_BGPPeerIsolation(t *testing.T) {
	pfx := netip.MustParsePrefix("10.0.0.0/24")
	nh := netip.MustParseAddr("192.0.2.1")
	peer1 := netip.MustParseAddr("10.1.1.1")
	peer2 := netip.MustParseAddr("10.1.1.2")

	r := newTestRIB(t)
	sessionID := r.NewSession()

	require.True(t, r.Update(
		sessionID,
		Route{Prefix: pfx, NextHop: nh, Peer: peer1, SourceID: RouteSourceBird},
		Route{Prefix: pfx, NextHop: nh, Peer: peer2, SourceID: RouteSourceBird},
	))

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

// Test_RIB_CleanupTask_MultiPathPrefixPartiallyStale verifies that a cleanup
// pass drops every expired path of a prefix and keeps the later-session one.
//
// Cleanup walks the routes of one prefix while the removal of an earlier path
// shifts the very slice it walks, so the surviving path must be neither
// skipped nor mistaken for a removal candidate.
func Test_RIB_CleanupTask_MultiPathPrefixPartiallyStale(t *testing.T) {
	pfx := netip.MustParsePrefix("2001:db8::/64")
	fresh := netip.MustParseAddr("2001:db8::d")

	r := newTestRIB(t)

	// Two real sessions, so the earlier three paths and the later one carry
	// the session ids their own Update call actually fenced on.
	oldSession := r.NewSession()
	require.True(t, r.Update(
		oldSession,
		birdRoute(pfx, "2001:db8::a", 1, oldSession),
		birdRoute(pfx, "2001:db8::b", 2, oldSession),
		birdRoute(pfx, "2001:db8::c", 3, oldSession),
	))

	newSession := r.NewSession()
	require.True(t, r.Update(newSession, birdRoute(pfx, fresh.String(), 4, newSession)))

	require.Len(t, routesForPrefix(t, r, pfx), 4, "setup: all four paths must be present")

	runCleanup(t, r, oldSession)

	require.Equal(t, []netip.Addr{fresh}, peersForPrefix(t, r, pfx),
		"only the path of the later session may survive a cleanup of the earlier one")
}

// Test_RIB_CleanupTask_MultiPathPrefixFullyStale verifies that a cleanup pass
// removes the prefix entirely once every one of its BGP paths has expired.
func Test_RIB_CleanupTask_MultiPathPrefixFullyStale(t *testing.T) {
	pfx := netip.MustParsePrefix("2001:db8::/64")

	r := newTestRIB(t)
	sessionID := r.NewSession()
	require.True(t, r.Update(
		sessionID,
		birdRoute(pfx, "2001:db8::a", 1, 1),
		birdRoute(pfx, "2001:db8::b", 2, 1),
		birdRoute(pfx, "2001:db8::c", 3, 1),
	))
	require.Len(t, routesForPrefix(t, r, pfx), 3, "setup: all three paths must be present")

	runCleanup(t, r, 1)

	require.Nil(t, routesForPrefix(t, r, pfx),
		"prefix entry must be absent once all of its paths expired")
}

// Test_RIB_CleanupTask_KeepsStaticRoute verifies that a cleanup pass leaves a
// static route sharing a prefix with expired BGP paths in place.
//
// Staleness is scoped to BIRD-sourced routes, and a static route carries no
// session, so no session number may ever expire it.
func Test_RIB_CleanupTask_KeepsStaticRoute(t *testing.T) {
	pfx := netip.MustParsePrefix("2001:db8::/64")
	nh := netip.MustParseAddr("2001:db8::ffff")

	r := newTestRIB(t)
	sessionID := r.NewSession()
	require.True(t, r.Update(
		sessionID,
		birdRoute(pfx, "2001:db8::a", 1, 1),
		birdRoute(pfx, "2001:db8::b", 2, 1),
	))
	require.NoError(t, r.AddUnicastRoute(pfx, nh, RouteSourceStatic))

	runCleanup(t, r, 1)

	routes := routesForPrefix(t, r, pfx)
	require.Len(t, routes, 1, "the static route must be the only survivor")
	require.Equal(t, RouteSourceStatic, routes[0].SourceID)
	require.Equal(t, nh, routes[0].NextHop)
}

// Test_RIB_CleanupTask_StatsMatchContents verifies that the counters reported
// after a cleanup pass agree with what the RIB actually holds.
func Test_RIB_CleanupTask_StatsMatchContents(t *testing.T) {
	stale := netip.MustParsePrefix("2001:db8:1::/64")
	mixed := netip.MustParsePrefix("2001:db8:2::/64")

	r := newTestRIB(t)

	// Two real sessions, so the earlier three routes and the later one carry
	// the session ids their own Update call actually fenced on.
	oldSession := r.NewSession()
	require.True(t, r.Update(
		oldSession,
		birdRoute(stale, "2001:db8::a", 1, oldSession),
		birdRoute(stale, "2001:db8::b", 2, oldSession),
		birdRoute(mixed, "2001:db8::a", 3, oldSession),
	))

	newSession := r.NewSession()
	require.True(t, r.Update(newSession, birdRoute(mixed, "2001:db8::b", 4, newSession)))

	runCleanup(t, r, oldSession)

	prefixes, routes := countDump(t, r)
	stats := r.Stats()
	require.Equal(t, prefixes, stats.Prefixes, "prefix counter must match the stored prefixes")
	require.Equal(t, routes, stats.Routes, "route counter must match the stored routes")
}

// Test_RIB_NewSession_ConcurrentOrdering verifies that every round of
// concurrent NewSession calls hands out unique, contiguous ids where only
// the round's greatest id can still write.
//
// A write tagged with any other id in the round is rejected without
// mutating the RIB. Allocating an id and publishing it as active must be
// one atomic step — a counter increment followed by a separate store could
// let two concurrent callers finish with a smaller id published last,
// leaving the greatest id unable to write. This is a logic assertion, not
// a data race: it is observed, not detected, under concurrent scheduling,
// so the round and goroutine counts are sized to make the defect show up
// reliably.
func Test_RIB_NewSession_ConcurrentOrdering(t *testing.T) {
	const goroutines = 16
	const rounds = 500

	pfx := netip.MustParsePrefix("2001:db8::/64")
	peer := "2001:db8::a"

	r := newTestRIB(t)
	// LongestMatch copies only the matched list, keeping each round's check
	// cheap across many rounds.
	activeRoutes := func() []Route {
		_, list, ok := r.LongestMatch(netip.MustParseAddr("2001:db8::1"))
		require.True(t, ok)
		return list.Routes
	}

	var prevMax uint64
	for range rounds {
		ids := make([]uint64, goroutines)

		var g errgroup.Group
		for idx := range goroutines {
			g.Go(func() error {
				ids[idx] = r.NewSession()
				return nil
			})
		}
		require.NoError(t, g.Wait())

		seen := make(map[uint64]bool, goroutines)
		maxID := uint64(0)
		for _, id := range ids {
			require.False(t, seen[id], "each id handed out in a round must be unique")
			seen[id] = true
			if id > maxID {
				maxID = id
			}
		}
		for offset := uint64(1); offset <= goroutines; offset++ {
			require.True(t, seen[prevMax+offset],
				"a round's ids must be contiguous with the previous round's greatest id")
		}

		require.True(t, r.Update(maxID, birdRoute(pfx, peer, 1, maxID)),
			"the round's greatest id must still be able to write")

		for _, id := range ids {
			if id == maxID {
				continue
			}
			require.False(t, r.Update(id, birdRoute(pfx, peer, 1, id)),
				"any id other than the round's greatest must be rejected")
		}

		routes := activeRoutes()
		require.Len(t, routes, 1, "a rejected write must never mutate the RIB")
		require.Equal(t, maxID, routes[0].SessionID,
			"the stored route must still carry the round's greatest id")

		prevMax = maxID
	}
}

// Test_RIB_Update_SupersededSessionRejected verifies that an announce or
// withdraw tagged with a superseded session id is rejected.
//
// The active session's same-identity route is left untouched by it, and a
// cleanup pass targeting the superseded id still leaves that route in
// place.
func Test_RIB_Update_SupersededSessionRejected(t *testing.T) {
	pfx := netip.MustParsePrefix("2001:db8::/64")
	peer := "2001:db8::a"

	cases := []struct {
		name string
		// lateUpdate builds the superseded session's late send, tagged with
		// sessionA, for the same identity as the active session's route.
		lateUpdate func(sessionA uint64) Route
	}{
		{
			name: "announce",
			lateUpdate: func(sessionA uint64) Route {
				stale := birdRoute(pfx, peer, 1, sessionA)
				stale.Pref = 100
				return stale
			},
		},
		{
			name: "withdraw",
			lateUpdate: func(sessionA uint64) Route {
				stale := birdRoute(pfx, peer, 1, sessionA)
				stale.ToRemove = true
				return stale
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRIB(t)
			sessionA := r.NewSession()
			sessionB := r.NewSession()

			active := birdRoute(pfx, peer, 1, sessionB)
			active.Pref = 200
			require.True(t, r.Update(sessionB, active))

			require.False(t, r.Update(sessionA, tc.lateUpdate(sessionA)),
				"a write tagged with a superseded session must be rejected")

			routes := routesForPrefix(t, r, pfx)
			require.Len(t, routes, 1)
			require.Equal(t, sessionB, routes[0].SessionID, "the active session's route must keep its own session id")
			require.Equal(t, uint32(200), routes[0].Pref, "the active session's route must keep its own attributes")

			runCleanup(t, r, sessionA)

			routes = routesForPrefix(t, r, pfx)
			require.Len(t, routes, 1, "cleanup targeting the superseded session must not remove the active session's route")
		})
	}
}

// Test_RIB_Update_SessionZeroRejected verifies that a write tagged with
// session id zero is always rejected, whether the RIB has never started a
// session or currently has one active.
func Test_RIB_Update_SessionZeroRejected(t *testing.T) {
	pfx := netip.MustParsePrefix("2001:db8::/64")

	t.Run("fresh RIB", func(t *testing.T) {
		r := newTestRIB(t)
		require.False(t, r.Update(0, birdRoute(pfx, "2001:db8::a", 1, 0)))
		require.Empty(t, routesForPrefix(t, r, pfx))
	})

	t.Run("active RIB", func(t *testing.T) {
		r := newTestRIB(t)
		r.NewSession()
		require.False(t, r.Update(0, birdRoute(pfx, "2001:db8::a", 1, 0)))
		require.Empty(t, routesForPrefix(t, r, pfx))
	})
}

// Test_RIB_IsActive verifies that IsActive reports the currently active
// session id true, a superseded id false, and id zero false whether the RIB
// has never started a session or currently has one active.
func Test_RIB_IsActive(t *testing.T) {
	t.Run("fresh RIB", func(t *testing.T) {
		r := newTestRIB(t)
		require.False(t, r.IsActive(0), "id zero must never be reported active")
	})

	r := newTestRIB(t)
	sessionA := r.NewSession()
	require.True(t, r.IsActive(sessionA), "the only session so far must be active")
	require.False(t, r.IsActive(0), "id zero must never be reported active")

	sessionB := r.NewSession()
	require.True(t, r.IsActive(sessionB), "the newest session must be active")
	require.False(t, r.IsActive(sessionA), "a superseded session must not be reported active")
	require.False(t, r.IsActive(0), "id zero must never be reported active")
}
