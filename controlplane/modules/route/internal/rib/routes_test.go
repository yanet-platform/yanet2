package rib

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRouteComparator(t *testing.T) {
	a, b, c := MakeBirdRoute(), MakeBirdRoute(), MakeBirdRoute()
	a.Prefix = netip.MustParsePrefix("::aaaa/128")
	b.Prefix = netip.MustParsePrefix("::bbbb/128")
	c.Prefix = netip.MustParsePrefix("::cccc/128")

	s := func(routes ...*Route) string {
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
	require.True(t, routeCompare(a, b) > 0)
	require.True(t, routeCompare(c, a) > 0)
	require.True(t, routeCompare(c, b) > 0)

	routes := []*Route{a, b, c}
	slices.SortFunc(routes, routeCompareRev)
	// c hash ASPathLen == 0 so it is the best route now!
	require.Equal(t, s(c, a, b), s(routes...))

	b.Pref = 100
	slices.SortFunc(routes, routeCompareRev)
	require.Equal(t, s(b, c, a), s(routes...))
}

func TestRoutesList_Insert_keepsBest(t *testing.T) {
	r1 := MakeBirdRoute()
	r2 := MakeBirdRoute()
	r3 := MakeBirdRoute()
	for idx, r := range []*Route{r1, r2, r3} {
		r.Peer = netip.MustParseAddr(fmt.Sprintf("0.0.0.%d", idx))
		r.Pref = 100
	}

	list := RoutesList{}
	require.Equal(t, (*Route)(nil), list.Best())

	a := *r1
	list.Insert(&a)
	require.Equal(t, r1, list.Best())
	require.Len(t, list.Routes, 1)

	b := *r2
	b.Pref = 200 // the best
	list.Insert(&b)
	require.Equal(t, b, *list.Best())
	require.Len(t, list.Routes, 2)

	c := *r3
	list.Insert(&c)
	require.Equal(t, b, *list.Best())
	require.Len(t, list.Routes, 3)

	d := *r3
	d.Pref = 300
	list.Insert(&d)
	require.Equal(t, d, *list.Best())
	require.Len(t, list.Routes, 3)

	e := *r3
	list.Insert(&e)
	require.Equal(t, b, *list.Best())
	require.Len(t, list.Routes, 3)
}

func TestRouteList_Remove_keepsBest(t *testing.T) {
	r1ref := MakeBirdRoute()
	r2ref := MakeBirdRoute()
	r3ref := MakeBirdRoute()
	list := RoutesList{}
	for idx, r := range []*Route{r1ref, r2ref, r3ref} {
		r.Peer = netip.MustParseAddr(fmt.Sprintf("0.0.0.%d", idx))
		r.Pref = 100 * uint32(idx)
		rCopy := *r
		list.Insert(&rCopy)
	}

	best := *r3ref
	require.Equal(t, best, *list.Best())

	list.Remove(&best)

	best = *r2ref
	require.Equal(t, best, *list.Best())

	newBest := *r3ref
	list.Routes = append(list.Routes, &newBest) // newBest is not at the index 0

	list.Remove(&best)                      // remove r2ref
	require.Equal(t, newBest, *list.Best()) // now newBest is the best

	require.True(t, !slices.Contains(list.Routes, &best))
	list.Remove(&best)                      // removing missing route is noOp
	require.Equal(t, newBest, *list.Best()) // newBest is still the best
}
