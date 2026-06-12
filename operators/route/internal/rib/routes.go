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

// isSameIdentity reports whether two routes share the same RIB identity.
//
// BGP-sourced routes are identified by peer because of BGP implicit replace:
// a peer re-announcing a prefix with a new nexthop must replace its previous
// path (RFC 4271), so BGP ECMP arises across peers. Static routes are
// peerless independent entries, so their identity includes the nexthop,
// allowing multiple static routes for the same prefix with distinct nexthops
// to coexist. Static nexthops are compared in normalized (unmapped) form
// because the API accepts both native IPv4 and IPv4-in-IPv6 encodings.
func (m Route) isSameIdentity(other Route) bool {
	if m.SourceID != other.SourceID || m.Peer != other.Peer {
		return false
	}
	if m.SourceID == RouteSourceStatic {
		return m.NextHop.Unmap() == other.NextHop.Unmap()
	}
	return true
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
	// lower MED is better (RFC 4271 §9.1.2.2)
	return int(b.Med) - int(a.Med)
}

func routeCompareRev(a Route, b Route) int {
	return -routeCompare(a, b)
}

type RoutesList struct {
	Routes []Route
}

// Insert adds or replaces a route in the list.
//
// Returns true when a new route was appended, false when an existing route was
// replaced.
func (m *RoutesList) Insert(route Route) bool {
	insertedIdx := -1
	for idx, r := range m.Routes {
		if r.isSameIdentity(route) {
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
	return insertedIdx == -1
}

// BestPerSourceMask returns a boolean slice, aligned index-for-index with
// Routes, where true means the route at that index is a member of its
// source's best-cost group.
//
// The list is assumed to be sorted best-first by routeCompareRev. For each
// source, the first route seen establishes the best cost; every subsequent
// route of the same source with equal cost is also marked true. Routes
// strictly worse than their source's best are false.
func (m *RoutesList) BestPerSourceMask() []bool {
	mask := make([]bool, len(m.Routes))
	best := map[RouteSourceID]Route{}

	for idx, r := range m.Routes {
		b, seen := best[r.SourceID]
		if !seen {
			best[r.SourceID] = r
			mask[idx] = true
			continue
		}
		if routeCompare(r, b) == 0 {
			mask[idx] = true
		}
	}
	return mask
}

// BestPerSource returns the best-cost group of routes for each distinct
// SourceID.
//
// The list is assumed to be sorted best-first by routeCompareRev. For each
// source, the method takes the first (best) route and every other route of
// the same source whose cost is equal to that source's best cost. The returned
// slice preserves the original order, which is deterministic because the input
// slice is always sorted.
func (m *RoutesList) BestPerSource() []Route {
	mask := m.BestPerSourceMask()

	var result []Route
	for idx, r := range m.Routes {
		if mask[idx] {
			result = append(result, r)
		}
	}
	return result
}

func (m *RoutesList) Remove(route Route) bool {
	// Sorting is not need on removing
	for idx, r := range m.Routes {
		if r.isSameIdentity(route) {
			// Delete with preserving order
			m.Routes = slices.Delete(m.Routes, idx, idx+1)

			return true
		}
	}

	return false
}
