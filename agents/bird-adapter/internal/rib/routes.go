package rib

import (
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
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

func convertLargeCommunity(community LargeCommunity) *routepb.LargeCommunity {
	return &routepb.LargeCommunity{
		GlobalAdministrator: community.GlobalAdministrator,
		LocalDataPart1:      community.LocalDataPart1,
		LocalDataPart2:      community.LocalDataPart2,
	}
}

func ToPBRoute(route *Route, isBest bool) *routepb.Route {
	communities := make([]*routepb.LargeCommunity, len(route.LargeCommunities))
	for _, c := range route.LargeCommunities {
		communities = append(communities, convertLargeCommunity(c))
	}

	peer := ""
	if route.Peer.IsValid() {
		peer = route.Peer.String()
	}

	return &routepb.Route{
		Prefix:           route.Prefix.String(),
		NextHop:          route.NextHop.String(),
		Peer:             peer,
		PeerAs:           route.PeerAS,
		OriginAs:         route.OriginAS,
		Med:              route.Med,
		Pref:             route.Pref,
		Source:           routepb.RouteSourceID(route.SourceID),
		LargeCommunities: communities,
		IsBest:           isBest,
	}
}
