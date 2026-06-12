package rib

import (
	"net/netip"
	"slices"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestRouteComparator(t *testing.T) {
	a, b, c := Route{}, Route{}, Route{}
	a.Prefix = netip.MustParsePrefix("::aaaa/128")
	b.Prefix = netip.MustParsePrefix("::bbbb/128")
	c.Prefix = netip.MustParsePrefix("::cccc/128")

	s := func(routes ...Route) string {
		ret := ""
		for idx, r := range routes {
			if idx > 0 {
				ret += ", "
			}
			ret += r.Prefix.String()
		}
		return ret
	}

	require.True(t, routeCompare(a, b) == 0, "routeCompare(%s, %s) != 0", s(a), s(b))

	a.Pref = 100 // x is the best now
	require.True(t, routeCompare(a, b) > 0)

	a.Pref = 0
	a.ASPathLen = 2
	require.True(t, routeCompare(a, b) < 0)

	b.ASPathLen = 2
	a.Med = 1
	// a has Med=1, b has Med=0; lower MED is better, so b beats a
	require.True(t, routeCompare(a, b) < 0)
	// c has ASPathLen=0 which beats both a and b (ASPathLen=2)
	require.True(t, routeCompare(c, a) > 0)
	require.True(t, routeCompare(c, b) > 0)

	routes := []Route{a, b, c}
	slices.SortFunc(routes, routeCompareRev)
	// c has ASPathLen=0 so it is the best; among equal ASPathLen, b (Med=0) beats a (Med=1)
	require.Equal(t, s(c, b, a), s(routes...))

	b.Pref = 100
	routes = []Route{a, b, c}
	slices.SortFunc(routes, routeCompareRev)
	require.Equal(t, s(b, c, a), s(routes...))
}

// TestRoutesListStaticECMP verifies that static routes with distinct nexthops
// coexist as ECMP entries while a re-announced nexthop replaces in place.
func TestRoutesListStaticECMP(t *testing.T) {
	pfx := netip.MustParsePrefix("10.0.0.0/24")
	nh1 := netip.MustParseAddr("10.0.0.1")
	nh2 := netip.MustParseAddr("10.0.0.2")
	unspec := netip.IPv6Unspecified()

	t.Run("two static routes different nexthops both present", func(t *testing.T) {
		var list RoutesList
		r1 := Route{Prefix: pfx, NextHop: nh1, Peer: unspec, SourceID: RouteSourceStatic}
		r2 := Route{Prefix: pfx, NextHop: nh2, Peer: unspec, SourceID: RouteSourceStatic}

		added1 := list.Insert(r1)
		added2 := list.Insert(r2)

		require.True(t, added1, "first static route should be added")
		require.True(t, added2, "second static route with different nexthop should be added")
		require.Len(t, list.Routes, 2)
	})

	t.Run("re-insert same static nexthop replaces in place", func(t *testing.T) {
		var list RoutesList
		r1 := Route{Prefix: pfx, NextHop: nh1, Peer: unspec, SourceID: RouteSourceStatic}
		r1Updated := Route{Prefix: pfx, NextHop: nh1, Peer: unspec, SourceID: RouteSourceStatic, Pref: 100}

		list.Insert(r1)
		added := list.Insert(r1Updated)

		require.False(t, added, "re-insert of same nexthop should return false")
		require.Len(t, list.Routes, 1)
		require.Equal(t, uint32(100), list.Routes[0].Pref)
	})

	t.Run("remove one static nexthop leaves the other", func(t *testing.T) {
		var list RoutesList
		r1 := Route{Prefix: pfx, NextHop: nh1, Peer: unspec, SourceID: RouteSourceStatic}
		r2 := Route{Prefix: pfx, NextHop: nh2, Peer: unspec, SourceID: RouteSourceStatic}
		list.Insert(r1)
		list.Insert(r2)

		removed := list.Remove(r1)

		require.True(t, removed)
		require.Len(t, list.Routes, 1)
		require.Equal(t, nh2, list.Routes[0].NextHop)
	})

	t.Run("ipv4-mapped encoding treated as same nexthop", func(t *testing.T) {
		mapped := netip.MustParseAddr("::ffff:10.0.0.1")
		var list RoutesList
		r1 := Route{Prefix: pfx, NextHop: nh1, Peer: unspec, SourceID: RouteSourceStatic}
		rMapped := Route{Prefix: pfx, NextHop: mapped, Peer: unspec, SourceID: RouteSourceStatic}

		list.Insert(r1)
		added := list.Insert(rMapped)

		require.False(t, added, "mapped encoding of same nexthop should replace, not append")
		require.Len(t, list.Routes, 1)
	})

	t.Run("removal via ipv4-mapped encoding removes native entry", func(t *testing.T) {
		mapped := netip.MustParseAddr("::ffff:10.0.0.1")
		var list RoutesList
		r1 := Route{Prefix: pfx, NextHop: nh1, Peer: unspec, SourceID: RouteSourceStatic}

		list.Insert(r1)
		removed := list.Remove(Route{Prefix: pfx, NextHop: mapped, Peer: unspec, SourceID: RouteSourceStatic})

		require.True(t, removed, "remove with mapped encoding must find the native entry")
		require.Empty(t, list.Routes)
	})

	t.Run("bird implicit replace preserves list length", func(t *testing.T) {
		birdPeer := netip.MustParseAddr("192.0.2.1")
		var list RoutesList
		r1 := Route{Prefix: pfx, NextHop: nh1, Peer: birdPeer, SourceID: RouteSourceBird}
		r2 := Route{Prefix: pfx, NextHop: nh2, Peer: birdPeer, SourceID: RouteSourceBird}

		list.Insert(r1)
		added := list.Insert(r2)

		require.False(t, added, "bird re-announce from same peer should replace, not append")
		require.Len(t, list.Routes, 1)
		require.Equal(t, nh2, list.Routes[0].NextHop)
	})
}

// TestBestPerSource verifies that BestPerSource returns the equal-cost group
// for each source and filters routes that are strictly worse than the source's best.
func TestBestPerSource(t *testing.T) {
	pfx := netip.MustParsePrefix("10.0.0.0/24")
	unspec := netip.IPv6Unspecified()
	p1 := netip.MustParseAddr("192.0.2.1")
	p2 := netip.MustParseAddr("192.0.2.2")

	t.Run("routes with different Pref only best group survives", func(t *testing.T) {
		list := RoutesList{
			Routes: []Route{
				// best (Pref=200)
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: RouteSourceBird, Pref: 200},
				// worse (Pref=100)
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: RouteSourceBird, Pref: 100},
			},
		}
		slices.SortFunc(list.Routes, routeCompareRev)

		best := list.BestPerSource()
		require.Len(t, best, 1)
		require.Equal(t, netip.MustParseAddr("10.0.0.1"), best[0].NextHop)
	})

	t.Run("equal-cost routes from different peers all included", func(t *testing.T) {
		list := RoutesList{
			Routes: []Route{
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: RouteSourceBird, Pref: 100},
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: RouteSourceBird, Pref: 100},
			},
		}
		slices.SortFunc(list.Routes, routeCompareRev)

		best := list.BestPerSource()
		require.Len(t, best, 2)
	})

	t.Run("static and bird on one prefix both groups in result", func(t *testing.T) {
		list := RoutesList{
			Routes: []Route{
				// bird route is the overall best
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: RouteSourceBird, Pref: 200},
				// static route has lower overall Pref but is its own source
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.2"), Peer: unspec, SourceID: RouteSourceStatic, Pref: 100},
			},
		}
		slices.SortFunc(list.Routes, routeCompareRev)

		best := list.BestPerSource()
		require.Len(t, best, 2, "both sources should appear in the best-per-source union")
	})

	t.Run("lower MED route wins when Pref and ASPathLen are equal", func(t *testing.T) {
		list := RoutesList{
			Routes: []Route{
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: RouteSourceBird, Pref: 100, Med: 10},
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: RouteSourceBird, Pref: 100, Med: 20},
			},
		}
		slices.SortFunc(list.Routes, routeCompareRev)

		best := list.BestPerSource()
		require.Len(t, best, 1, "only the lower-MED route should survive")
		require.Equal(t, netip.MustParseAddr("10.0.0.1"), best[0].NextHop)
		require.Equal(t, uint32(10), best[0].Med)
	})

	t.Run("all equal-cost nothing filtered", func(t *testing.T) {
		list := RoutesList{
			Routes: []Route{
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.1"), Peer: p1, SourceID: RouteSourceBird, Pref: 100},
				{Prefix: pfx, NextHop: netip.MustParseAddr("10.0.0.2"), Peer: p2, SourceID: RouteSourceBird, Pref: 100},
			},
		}
		slices.SortFunc(list.Routes, routeCompareRev)

		best := list.BestPerSource()
		require.Len(t, best, len(list.Routes))
	})
}

func TestRoutesListDeletion(t *testing.T) {
	list := RoutesList{
		Routes: []Route{
			{Prefix: netip.MustParsePrefix("::a1/128")},
			{Prefix: netip.MustParsePrefix("::a2/128")},
			{Prefix: netip.MustParsePrefix("::a2/128"), ToRemove: true},
		},
	}

	listCopy := RoutesList{
		Routes: slices.Clone(list.Routes),
	}
	require.True(t, unsafe.Pointer(&list.Routes[0]) != unsafe.Pointer(&listCopy.Routes[0]))

	routesTruncated := slices.Delete(list.Routes, 2, 3)

	require.True(t, listCopy.Routes[2].ToRemove)
	require.False(t, list.Routes[2].ToRemove)
	require.Equal(t, list.Routes[2], Route{}) // zeroed by slices.Delete
	require.Equal(t, 2, len(routesTruncated))

}
