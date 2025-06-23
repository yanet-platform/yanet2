package routepb

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/modules/route/internal/rib"
)

func FromRIBRoute(route *rib.Route, isBest bool) *Route {
	communities := make([]*LargeCommunity, len(route.LargeCommunities))
	for _, c := range route.LargeCommunities {
		communities = append(communities, convertLargeCommunity(c))
	}

	peer := ""
	if route.Peer.IsValid() {
		peer = route.Peer.String()
	}

	return &Route{
		Prefix:           route.Prefix.String(),
		NextHop:          route.NextHop.String(),
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

func ToRIBRoute(route *Route, toRemove bool) (*rib.Route, error) {
	if route == nil {
		return nil, fmt.Errorf("update.Route cannot be nil")
	}
	prefix, err := netip.ParsePrefix(route.GetPrefix())
	if err != nil {
		return nil, err
	}
	nexthop, err := netip.ParseAddr(route.GetNextHop())
	if err != nil {
		return nil, err
	}

	peer, err := netip.ParseAddr(route.GetPeer())
	if err != nil {
		return nil, err
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
