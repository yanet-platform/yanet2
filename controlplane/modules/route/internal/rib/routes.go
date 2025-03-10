package rib

import (
	"net/netip"
	"slices"
	"sync"
	"time"
)

// Pool for the Route structs
var (
	routeStructPool sync.Pool = sync.Pool{
		New: func() any {
			return &Route{}
		},
	}
)

func FreeRoute(r *Route) {
	*r = Route{} // clear
	routeStructPool.Put(r)
}

func makeRoute() *Route {
	r := routeStructPool.Get().(*Route)
	r.UpdatedAt = time.Now()
	return r
}

func MakeStaticRoute() *Route {
	r := makeRoute()
	r.SourceID = RouteSourceStatic
	return r
}

func MakeBirdRoute() *Route {
	r := makeRoute()
	r.SourceID = RouteSourceBird
	return r
}

type RouteSourceID uint8

const (
	RouteSourceUnknown RouteSourceID = iota
	RouteSourceStatic
	RouteSourceBird
)

type Route struct {
	Prefix    netip.Prefix
	NextHop   netip.Addr
	Peer      netip.Addr
	RD        uint64
	UpdatedAt time.Time
	PeerAS    uint32
	OriginAS  uint32
	Med       uint32
	Pref      uint32
	ASPathLen uint8
	SourceID  RouteSourceID
	ToRemove  bool
}

func routeCompare(a *Route, b *Route) int {
	// higher priority is better
	if prefDiff := int(a.Pref) - int(b.Pref); prefDiff != 0 {
		return prefDiff
	}

	// check ASPathLen in reverse order, shortest path is better
	if asPathLenDiff := int(b.ASPathLen) - int(a.ASPathLen); asPathLenDiff != 0 {
		return asPathLenDiff
	}
	return int(a.Med) - int(b.Med)
}

func routeCompareRev(a *Route, b *Route) int {
	return -routeCompare(a, b)
}

type RoutesList struct {
	Routes []*Route
}

func (m *RoutesList) Insert(route *Route) bool {
	insertedIdx := -1
	for idx, r := range m.Routes {
		if r.Peer == route.Peer {
			FreeRoute(m.Routes[idx]) // release updated route
			m.Routes[idx] = route
			insertedIdx = idx
			break
		}
	}
	if insertedIdx == -1 {
		m.Routes = append(m.Routes, route)
	}
	if len(m.Routes) > 1 {
		// Sorting an almost-sorted slice should be relatively efficient
		slices.SortFunc(m.Routes, routeCompareRev) // for DESC order
	}
	return true
}

func (m *RoutesList) Remove(route *Route) bool {
	defer FreeRoute(route)
	// Sorting is not need on removing
	for idx, r := range m.Routes {
		if r.Peer == route.Peer {
			FreeRoute(m.Routes[idx]) // relese deleted route too
			// Delete with preserving order
			m.Routes = slices.Delete(m.Routes, idx, idx+1)

			return true
		}
	}

	return false
}
