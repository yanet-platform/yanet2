package rib

import (
	"net/netip"
	"slices"
	"time"
)

type RouteSourceID uint8

const (
	RouteSourceUnknown RouteSourceID = iota
	RouteSourceStatic
	RouteSourceBird
)

type LargeCommunity struct {
	GlobalAdministrator uint32
	LocalDataPart1      uint32
	LocalDataPart2      uint32
}

// Route stores information about network routes and associated BGP attributes.
// This information helps compute route costs, enabling the data plane to select
// the optimal route for traffic forwarding
type Route struct {
	// SessionID identifies the session that added this route to the rib, used
	// for cleanup of stale routes.
	SessionID uint64
	// Prefix is used as a key in routing databases (RIB and FIB), represented as
	// an IPv6 network prefix. IPv4 addresses are stored as IPv6-mapped addresses.
	Prefix netip.Prefix
	// NextHop is the IP address where traffic should be forwarded next.
	NextHop netip.Addr
	// Peer is the IP address of the BGP peer that advertised this route.
	//
	// This field is used to distinguish similar routes from different peers.
	// The same routes can have different costs.
	Peer netip.Addr
	// RD stands for Route Distinguisher, as defined in RFC 4364.
	//
	// This field is used to distinguish similar routes to different systems.
	RD uint64
	// LargeCommunities is used for link bandwidth information.
	LargeCommunities []LargeCommunity
	// UpdatedAt notes the last time the route was added or modified in the RIB.
	UpdatedAt time.Time
	// PeerAS denotes the Autonomous System of the BGP peer, per RFC 4271 Section 5.1.2.
	PeerAS uint32
	// OriginAS indicates the Autonomous System where the route originated.
	OriginAS uint32
	// Med (MULTI_EXIT_DISC) guides route selection, per RFC 4271 Section 5.1.4.
	//
	// This field participates in route cost calculation.
	Med uint32
	// Pref refers to Local Preference, influencing route choice,
	// as per RFC 4271 Section 5.1.5.
	//
	// This field participates in route cost calculation.
	Pref uint32
	// ASPathLen measures the number of AS hops to reach our system.
	//
	// This field participates in route cost calculation.
	ASPathLen uint8
	// SourceID identifies the origin of this route's information,
	// such as static or Bird.
	SourceID RouteSourceID
	// ToRemove signals whether the route has been withdrawn from the routing table.
	ToRemove bool
}

func routeCompare(a Route, b Route) int {
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

func routeCompareRev(a Route, b Route) int {
	return -routeCompare(a, b)
}

type RoutesList struct {
	Routes []Route
}

func (m *RoutesList) Insert(route Route) bool {
	insertedIdx := -1
	for idx, r := range m.Routes {
		if r.Peer == route.Peer {
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

func (m *RoutesList) Remove(route Route) bool {
	// Sorting is not need on removing
	for idx, r := range m.Routes {
		if r.Peer == route.Peer {
			// Delete with preserving order
			m.Routes = slices.Delete(m.Routes, idx, idx+1)

			return true
		}
	}

	return false
}
