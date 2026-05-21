package operatorpb

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

// FromRIBRoute converts an internal rib.Route to the wire Route message.
func FromRIBRoute(route *rib.Route, isBest bool) *Route {
	communities := make([]*LargeCommunity, 0, len(route.LargeCommunities))
	for _, c := range route.LargeCommunities {
		communities = append(communities, convertLargeCommunity(c))
	}

	var peer *commonpb.IPAddress
	if route.Peer.IsValid() {
		peer = commonpb.NewIPAddressFromAddr(route.Peer)
	}

	return &Route{
		Prefix:           route.Prefix.String(),
		NextHop:          commonpb.NewIPAddressFromAddr(route.NextHop),
		Peer:             peer,
		PeerAs:           route.PeerAS,
		OriginAs:         route.OriginAS,
		Med:              route.Med,
		Pref:             route.Pref,
		Source:           RouteSourceID(route.SourceID),
		LargeCommunities: communities,
		IsBest:           isBest,
	}
}

func convertLargeCommunity(community rib.LargeCommunity) *LargeCommunity {
	return &LargeCommunity{
		GlobalAdministrator: community.GlobalAdministrator,
		LocalDataPart1:      community.LocalDataPart1,
		LocalDataPart2:      community.LocalDataPart2,
	}
}

// ToRIBRoute converts a wire Route message to the internal rib.Route
// representation.
func ToRIBRoute(route *Route, toRemove bool) (*rib.Route, error) {
	if route == nil {
		return nil, fmt.Errorf("update.Route cannot be nil")
	}
	prefix, err := netip.ParsePrefix(route.GetPrefix())
	if err != nil {
		return nil, err
	}
	nexthop, err := route.GetNextHop().ToAddr()
	if err != nil {
		return nil, fmt.Errorf("invalid next_hop (bytes=%x): %w", route.GetNextHop().GetAddr(), err)
	}

	peer, err := route.GetPeer().ToAddr()
	if err != nil {
		return nil, fmt.Errorf("invalid peer (bytes=%x): %w", route.GetPeer().GetAddr(), err)
	}
	largeCommunities := make([]rib.LargeCommunity, 0, len(route.LargeCommunities))
	for _, community := range route.LargeCommunities {
		largeCommunities = append(largeCommunities, rib.LargeCommunity{
			GlobalAdministrator: community.GetGlobalAdministrator(),
			LocalDataPart1:      community.GetLocalDataPart1(),
			LocalDataPart2:      community.GetLocalDataPart2(),
		})
	}

	sourceID := rib.RouteSourceUnknown
	switch route.GetSource() {
	case RouteSourceID_ROUTE_SOURCE_ID_BIRD:
		sourceID = rib.RouteSourceBird
	case RouteSourceID_ROUTE_SOURCE_ID_STATIC:
		sourceID = rib.RouteSourceStatic
	}

	return &rib.Route{
		Prefix:           prefix,
		NextHop:          nexthop,
		Peer:             peer,
		RD:               route.GetRouteDistinguisher(),
		LargeCommunities: largeCommunities,
		UpdatedAt:        time.Now(),
		PeerAS:           route.GetPeerAs(),
		OriginAS:         route.GetOriginAs(),
		Med:              route.GetMed(),
		Pref:             route.GetPref(),
		ASPathLen:        uint8(route.GetAsPathLen()),
		SourceID:         sourceID,
		ToRemove:         toRemove,
	}, nil
}

// RouteSourceID returns the internal rib.RouteSourceID for an
// InsertRouteRequest. Defaults to RouteSourceStatic.
func (m *InsertRouteRequest) RouteSourceID() rib.RouteSourceID {
	switch m.GetSourceId() {
	case RouteSourceID_ROUTE_SOURCE_ID_BIRD:
		return rib.RouteSourceBird
	default:
		return rib.RouteSourceStatic
	}
}

// RouteSourceID returns the internal rib.RouteSourceID for a
// DeleteRouteRequest. Defaults to RouteSourceStatic.
func (m *DeleteRouteRequest) RouteSourceID() rib.RouteSourceID {
	switch m.GetSourceId() {
	case RouteSourceID_ROUTE_SOURCE_ID_BIRD:
		return rib.RouteSourceBird
	default:
		return rib.RouteSourceStatic
	}
}
