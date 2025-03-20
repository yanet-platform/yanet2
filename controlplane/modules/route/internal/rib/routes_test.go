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
	require.True(t, routeCompare(a, b) > 0)
	require.True(t, routeCompare(c, a) > 0)
	require.True(t, routeCompare(c, b) > 0)

	routes := []Route{a, b, c}
	slices.SortFunc(routes, routeCompareRev)
	// c hash ASPathLen == 0 so it is the best route now!
	require.Equal(t, s(c, a, b), s(routes...))

	b.Pref = 100
	routes = []Route{a, b, c}
	slices.SortFunc(routes, routeCompareRev)
	require.Equal(t, s(b, c, a), s(routes...))
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
