package mpls

import (
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/agents/bird-adapter/internal/rib"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
)

// Route is identified by destination (nexthop) and route distinguisher
type mplsRouteKey struct {
	destination netip.Addr
	rd          uint64
}

// Each route may be accepted via bunch of peers so collect all the instances
type mplsPeerRouteList struct {
	routes map[mplsRouteKey][]rib.Route
}

type mplsRouteList struct {
	routes []rib.Route
}

type Rib struct {
	peerRoutes maptrie.MapTrie[netip.Prefix, netip.Addr, mplsPeerRouteList]
	routes     maptrie.MapTrie[netip.Prefix, netip.Addr, mplsRouteList]
}

func NewRib() Rib {
	return Rib{
		peerRoutes: maptrie.NewMapTrie[netip.Prefix, netip.Addr, mplsPeerRouteList](0),
		routes:     maptrie.NewMapTrie[netip.Prefix, netip.Addr, mplsRouteList](0),
	}
}

func (m mplsPeerRouteList) Withdraw(
	route rib.Route,
) (
	mplsPeerRouteList,
	*rib.Route,
) {
	key := mplsRouteKey{
		destination: route.NextHop,
		rd:          route.RD,
	}

	items := m.routes[key]
	if len(items) == 0 {
		/*
			No known route instance for destination::distinguisher - in
			some cases BGP may send withdraw without any route update
			before
		*/
		return m, nil
	}

	idx := slices.IndexFunc(items, func(known rib.Route) bool { return known.Peer == route.Peer })
	if idx == -1 {
		// The route was not announced before - nothing to do
		return m, nil
	}

	items = slices.Delete(items, idx, idx+1)
	if len(items) == 0 {
		delete(m.routes, key)
	} else {
		m.routes[key] = items
	}

	if idx == 0 {
		// The first one route was withdrawn so propagate the change
		return m, &route
	}

	return m, nil
}

func (m mplsPeerRouteList) Update(
	route rib.Route,
) (
	mplsPeerRouteList,
	*rib.Route,
) {
	key := mplsRouteKey{
		destination: route.NextHop,
		rd:          route.RD,
	}

	items := m.routes[key]

	idx := slices.IndexFunc(
		items,
		func(known rib.Route) bool {
			return known.Peer == route.Peer
		},
	)

	if idx == -1 {
		items = append(items, route)
	} else {
		items[idx] = route
	}

	slices.SortFunc(
		items,
		func(l rib.Route, r rib.Route) int {
			if l.Pref != r.Pref {
				return int(r.Pref) - int(l.Pref)
			}
			if len(l.ASPath) != len(r.ASPath) {
				return len(l.ASPath) - len(r.ASPath)
			}
			return int(l.Med) - int(r.Med)
		},
	)

	m.routes[key] = items
	if items[0].Peer != route.Peer {
		return m, nil
	}

	return m, &route
}

func routeListBest(
	routes []rib.Route,
) []rib.Route {
	if len(routes) < 2 {
		return routes[:len(routes)]
	}

	count := 1
	for count < len(routes) {
		if routes[count].Pref != routes[0].Pref ||
			len(routes[count].ASPath) != len(routes[0].ASPath) ||
			routes[count].Med != routes[0].Med {
			break
		}
		count++
	}

	return routes[:count]
}

func getLabel(route rib.Route) uint32 {
	if len(route.MplsLabelStack) == 0 {
		return 0
	}
	return route.MplsLabelStack[0]
}

func getWeight(route rib.Route) uint64 {
	for _, community := range route.LargeCommunities {
		if community.ASN == 13238 && community.Function == 1 {
			return uint64(community.Value)
		}
	}

	return 1
}

func routeListBestDiff(
	newRoutes []rib.Route,
	oldRoutes []rib.Route,
) []rib.Route {
	result := make([]rib.Route, 0)

	newBests := routeListBest(newRoutes)
	oldBests := routeListBest(oldRoutes)

	for idx := range oldBests {
		route := oldBests[idx]

		if slices.IndexFunc(
			newBests,
			func(known rib.Route) bool {
				return known.NextHop == route.NextHop &&
					known.RD == route.RD
			},
		) == -1 {
			route.ToRemove = true
			result = append(result, route)
		}
	}

	for idx := range newBests {
		route := newBests[idx]

		idx := slices.IndexFunc(
			oldBests,
			func(known rib.Route) bool {
				return known.NextHop == route.NextHop &&
					known.RD == route.RD
			},
		)

		if idx == -1 {
			result = append(result, route)
			continue
		}

		if getLabel(route) != getLabel(oldBests[idx]) ||
			getWeight(route) != getWeight(oldBests[idx]) {
			result = append(result, route)
		}
	}

	return result
}

func (m mplsRouteList) Withdraw(
	route rib.Route,
) (
	mplsRouteList,
	[]rib.Route,
) {
	oldRoutes := slices.Clone(m.routes)
	newRoutes := slices.DeleteFunc(
		m.routes,
		func(known rib.Route) bool {
			return known.NextHop == route.NextHop &&
				known.RD == route.RD
		},
	)

	m.routes = newRoutes
	return m, routeListBestDiff(newRoutes, oldRoutes)
}

func (m mplsRouteList) Update(
	route rib.Route,
) (
	mplsRouteList,
	[]rib.Route,
) {
	oldRoutes := slices.Clone(m.routes)

	newRoutes := m.routes
	idx := slices.IndexFunc(
		newRoutes,
		func(known rib.Route) bool {
			return known.NextHop == route.NextHop &&
				known.RD == route.RD
		},
	)

	if idx == -1 {
		newRoutes = append(newRoutes, route)
	} else {
		newRoutes[idx] = route
	}

	slices.SortFunc(
		newRoutes,
		func(l rib.Route, r rib.Route) int {
			if l.Pref != r.Pref {
				return int(r.Pref) - int(l.Pref)
			}
			if len(l.ASPath) != len(r.ASPath) {
				return len(l.ASPath) - len(r.ASPath)
			}
			return int(l.Med) - int(r.Med)
		},
	)

	m.routes = newRoutes
	return m, routeListBestDiff(newRoutes, oldRoutes)
}

func (m *Rib) Apply(route rib.Route) []rib.Route {
	key := mplsRouteKey{
		destination: route.NextHop,
		rd:          route.RD,
	}

	var peerRoute *rib.Route

	if route.ToRemove {
		m.peerRoutes.UpdateOrDelete(
			route.Prefix,
			func(m mplsPeerRouteList) (mplsPeerRouteList, bool) {
				m, peerRoute = m.Withdraw(route)
				return m, len(m.routes) == 0
			},
		)
	} else {
		m.peerRoutes.InsertOrUpdate(
			route.Prefix,
			func() mplsPeerRouteList {
				m := mplsPeerRouteList{
					routes: make(map[mplsRouteKey][]rib.Route),
				}
				m.routes[key] = []rib.Route{route}
				peerRoute = &route
				return m
			},
			func(m mplsPeerRouteList) mplsPeerRouteList {
				m, peerRoute = m.Update(route)

				return m
			},
		)
	}

	if peerRoute == nil {
		return nil
	}

	var updates []rib.Route

	if peerRoute.ToRemove {
		m.routes.UpdateOrDelete(
			route.Prefix,
			func(m mplsRouteList) (mplsRouteList, bool) {
				m, updates = m.Withdraw(*peerRoute)
				return m, len(m.routes) == 0
			},
		)
	} else {
		m.routes.InsertOrUpdate(
			route.Prefix,
			func() mplsRouteList {
				m := mplsRouteList{
					routes: []rib.Route{*peerRoute},
				}
				updates = append(updates, *peerRoute)

				return m
			},
			func(m mplsRouteList) mplsRouteList {
				m, updates = m.Update(*peerRoute)
				return m
			},
		)
	}

	return updates
}
