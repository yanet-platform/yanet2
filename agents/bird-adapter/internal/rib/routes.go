package rib

import (
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

type RouteSourceID uint8

const (
	RouteSourceUnknown RouteSourceID = iota
	RouteSourceStatic
	RouteSourceBird
)

type Community struct {
	ASN   uint16
	Value uint16
}

type ExtCommunity struct {
	Type    uint8
	SubType uint8
	Value   uint64
}

type LargeCommunity struct {
	ASN      uint32
	Function uint32
	Value    uint32
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
	// Communities are used to encode complementary route information
	Communities []Community
	// ExtCommunities are used to encode complementary route information
	ExtCommunities []ExtCommunity
	// LargeCommunities are used to encode complementary route information
	LargeCommunities []LargeCommunity
	// Med (MULTI_EXIT_DISC) guides route selection, per RFC 4271 Section 5.1.4.
	//
	// This field participates in route cost calculation.
	Med uint32
	// Pref refers to Local Preference, influencing route choice,
	// as per RFC 4271 Section 5.1.5.
	//
	// This field participates in route cost calculation.
	Pref uint32
	// Encode sequence of ASes to reach the target
	ASPath []uint32
	// Label stack corresponding to the route
	MplsLabelStack []uint32
	// Cluster list used to detect announce loops
	ClusterList []uint32
	// SourceID identifies the origin of this route's information,
	// such as static or Bird.
	SourceID RouteSourceID
	// ToRemove signals whether the route has been withdrawn from the routing table.
	ToRemove bool
}

func convertLargeCommunity(community LargeCommunity) *routepb.LargeCommunity {
	return &routepb.LargeCommunity{
		GlobalAdministrator: community.ASN,
		LocalDataPart1:      community.Function,
		LocalDataPart2:      community.Value,
	}
}

func ToPBRoute(route *Route) *routepb.Route {
	communities := make([]*routepb.LargeCommunity, 0, len(route.LargeCommunities))
	for _, c := range route.LargeCommunities {
		communities = append(communities, convertLargeCommunity(c))
	}

	peer := ""
	if route.Peer.IsValid() {
		peer = route.Peer.String()
	}

	peerAS := uint32(0)
	originAS := uint32(0)
	if len(route.ASPath) > 0 {
		peerAS = route.ASPath[0]
		originAS = route.ASPath[len(route.ASPath)-1]
	}

	return &routepb.Route{
		Prefix:           route.Prefix.String(),
		NextHop:          route.NextHop.String(),
		Peer:             peer,
		PeerAs:           peerAS,
		OriginAs:         originAS,
		Med:              route.Med,
		Pref:             route.Pref,
		Source:           routepb.RouteSourceID(route.SourceID),
		LargeCommunities: communities,
	}
}
