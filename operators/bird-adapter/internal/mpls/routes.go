package mpls

import (
	"maps"
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
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

// Withdraw drops the peer's announcement and returns the next best one when
// the best goes, the withdraw once no peer is left, or nil otherwise.
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
		return m, &route
	}

	m.routes[key] = items
	if idx == 0 {
		// The withdrawn announcement was the best, so the next one takes over.
		best := items[0]
		return m, &best
	}

	return m, nil
}

// Update records the peer's announcement and returns the best one when it
// may have changed, or nil when another peer's was the best and still is.
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
	var bestPeer netip.Addr
	if len(items) > 0 {
		bestPeer = items[0].Peer
	}

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
	if items[0].Peer != route.Peer && items[0].Peer == bestPeer {
		return m, nil
	}

	best := items[0]
	return m, &best
}

func routeListBest(
	routes []rib.Route,
) []rib.Route {
	if len(routes) < 2 {
		return routes[:]
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

// routeListBestDiff returns the route-mpls changes that turn the old best
// routes into the new ones.
//
// It withdraws every nexthop the new routes lack, then updates every route
// whose nexthop's routes changed. All routes sharing that nexthop are sent
// again, so the weight route-mpls keeps is one of theirs.
func routeListBestDiff(
	newRoutes []rib.Route,
	oldRoutes []rib.Route,
) []rib.Route {
	result := make([]rib.Route, 0)

	newBests := routeListBest(newRoutes)
	oldBests := routeListBest(oldRoutes)

	for idx := range oldBests {
		route := oldBests[idx]

		if slices.IndexFunc(newBests, sameNexthop(route)) == -1 {
			route.ToRemove = true
			result = append(result, route)
		}
	}

	for idx := range newBests {
		route := newBests[idx]

		if !maps.Equal(nexthopWeights(newBests, route), nexthopWeights(oldBests, route)) {
			result = append(result, route)
		}
	}

	return result
}

// sameNexthop matches the routes route-mpls holds as the same nexthop as
// the given one.
//
// route-mpls tells nexthops apart by address and label, not by the RD that
// tells routes apart here, so a route whose label changes is withdrawn
// under the old label and added under the new one.
func sameNexthop(route rib.Route) func(rib.Route) bool {
	return func(known rib.Route) bool {
		return known.NextHop == route.NextHop &&
			getLabel(known) == getLabel(route)
	}
}

// nexthopWeights returns the weight of every route sharing the given
// route's nexthop, keyed by RD.
func nexthopWeights(routes []rib.Route, route rib.Route) map[uint64]uint64 {
	matches := sameNexthop(route)
	weights := map[uint64]uint64{}
	for _, known := range routes {
		if matches(known) {
			weights[known.RD] = known.Weight()
		}
	}
	return weights
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

// Best returns the best routes of every prefix, the table a whole-table
// resend publishes.
func (m *Rib) Best() []rib.Route {
	var routes []rib.Route
	for _, list := range m.routes.Dump() {
		routes = append(routes, list.Best()...)
	}
	return routes
}

// Best returns the routes that tie for best: equal preference, AS-path
// length and MED.
func (m mplsRouteList) Best() []rib.Route {
	return routeListBest(m.routes)
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
