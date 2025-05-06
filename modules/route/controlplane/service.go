package route

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/bitset"
	"github.com/yanet-platform/yanet2/common/go/numa"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/discovery/bird"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/internal/rib"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

var (
	_ bird.RIBUpdater = (*RouteService)(nil)
)

type RouteService struct {
	routepb.UnimplementedRouteServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	flushCh chan flushEvent
	rib     *rib.RIB
	log     *zap.SugaredLogger
}

type flushEvent struct {
	moduleNames []string
	numaMap     numa.NUMAMap
}

func NewRouteService(agents []*ffi.Agent, rib *rib.RIB, log *zap.SugaredLogger) *RouteService {
	return &RouteService{
		agents: agents,
		// Buffer size of 2 provides minimal queuing capacity while preventing
		// blocking in most scenarios.
		flushCh: make(chan flushEvent, 2),
		rib:     rib,
		log:     log,
	}
}

func (m *RouteService) ShowRoutes(
	ctx context.Context,
	request *routepb.ShowRoutesRequest,
) (*routepb.ShowRoutesResponse, error) {
	routes := m.rib.DumpRoutes()
	response := &routepb.ShowRoutesResponse{}

	for prefix, routesList := range routes {
		if len(routesList.Routes) == 0 {
			continue
		}

		// Apply IPv4/IPv6 filters if specified.
		if request.Ipv4Only && !prefix.Addr().Is4() {
			continue
		}
		if request.Ipv6Only && !prefix.Addr().Is6() {
			continue
		}

		for idx, r := range routesList.Routes {
			isBest := idx == 0
			response.Routes = append(response.Routes, convertRoute(prefix, isBest, &r))
		}
	}

	return response, nil
}

func (m *RouteService) LookupRoute(
	ctx context.Context,
	request *routepb.LookupRouteRequest,
) (*routepb.LookupRouteResponse, error) {
	addr, err := netip.ParseAddr(request.GetIpAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse IP address: %v", err)
	}

	prefix, routes, ok := m.rib.LongestMatch(addr)
	if !ok {
		return &routepb.LookupRouteResponse{}, nil
	}

	response := &routepb.LookupRouteResponse{
		// TODO: Replace with IPNetwork protobuf message.
		Prefix: prefix.String(),
		Routes: make([]*routepb.Route, 0, len(routes.Routes)),
	}

	for idx, r := range routes.Routes {
		isBest := idx == 0
		response.Routes = append(response.Routes, convertRoute(prefix, isBest, &r))
	}

	return response, nil
}

func (m *RouteService) InsertRoute(
	ctx context.Context,
	request *routepb.InsertRouteRequest,
) (*routepb.InsertRouteResponse, error) {
	name := request.GetModuleName()

	prefix, err := netip.ParsePrefix(request.GetPrefix())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
	}

	nexthopAddr, err := netip.ParseAddr(request.GetNexthopAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop address: %v", err)
	}

	// After the intersection, the numaMap contains ONLY reachable NUMA nodes.
	numaMap := numa.NUMAMap(request.GetNuma()).Intersect(numa.NewWithTrailingOnes(len(m.agents)))
	if numaMap.IsEmpty() {
		return nil, status.Error(codes.InvalidArgument, "NUMA map is empty")
	}

	if err := m.rib.AddUnicastRoute(prefix, nexthopAddr); err != nil {
		return nil, fmt.Errorf("failed to add unicast route: %w", err)
	}

	return &routepb.InsertRouteResponse{}, m.syncRouteUpdates(name, numaMap)
}

func (m *RouteService) BulkUpdate(routes []rib.Route) error {
	m.log.Debugw("apply bulk update", zap.Int("size", len(routes)))
	start := time.Now()
	m.rib.BulkUpdate(routes)

	// If flushCh already contains events, it will trigger a RIB flush.
	// Therefore, we can safely skip adding a new event.
	select {
	case m.flushCh <- flushEvent{}:
	default:
	}

	m.log.Debugw("bulk update completed", zap.Stringer("took", time.Since(start)))
	return nil
}

// periodicRIBFlusher monitors and synchronizes route updates at regular intervals
// or when triggered by flush events. It runs until the context is canceled.
func (m *RouteService) periodicRIBFlusher(ctx context.Context, updatePeriod time.Duration) error {
	m.log.Infow("starting periodic route updates synchronization", zap.Stringer("period", updatePeriod))

	timer := time.NewTimer(updatePeriod)
	defer timer.Stop()

	var lastUpdate time.Time // Initialize zero time at startup
	for {
		var event flushEvent

		// Reset timer explicitly on each iteration
		timer.Reset(updatePeriod)

		// Wait for a trigger event or context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-timer.C:
			currentUpdate := m.rib.UpdatedAt()
			if !currentUpdate.After(lastUpdate) {
				// No changes since last update, skip this cycle
				continue
			}

			m.log.Debug("flushing RIB changes due to timeout")
			event = flushEvent{} // Empty event means process all modules
			lastUpdate = currentUpdate

		case evt, ok := <-m.flushCh:
			if !ok {
				return fmt.Errorf("flush events channel is closed")
			}

			m.log.Debugw("flushing RIB changes due to explicit event", zap.Any("event", evt))
			event = evt
		}

		// If no specific modules were requested, use all modules
		if len(event.moduleNames) == 0 {
			// FIXME: This should iterate over all available route modules
			event.moduleNames = append(event.moduleNames, "route0")
		}

		// Process updates for each requested module
		for _, name := range event.moduleNames {
			m.log.Debugw("synchronizing route updates", zap.String("module", name))

			if err := m.syncRouteUpdates(name, event.numaMap); err != nil {
				m.log.Warnw("failed to synchronize route updates",
					zap.String("module", name),
					zap.Error(err))
				// FIXME: continue with other modules even if one fails?
			}
		}
	}
}

func (m *RouteService) syncRouteUpdates(name string, numaMap numa.NUMAMap) error {
	routes := m.rib.DumpRoutes()

	// Huge mutex, but our shared memory must be protected from concurrent access.
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.updateModuleConfigs(name, numaMap, routes)
	return err
}

func (m *RouteService) updateModuleConfigs(
	name string,
	numaMap numa.NUMAMap,
	routes map[netip.Prefix]rib.RoutesList,
) error {
	configs := make([]*ModuleConfig, 0, numaMap.Len())

	for numaIdx := range numaMap.Iter() {
		agent := m.agents[numaIdx]

		config, err := NewModuleConfig(agent, name)
		if err != nil {
			return fmt.Errorf("failed to create %q module config: %w", name, err)
		}

		// Obtain neighbor entry with resolved hardware addresses
		neighbours := m.rib.NeighboursView()

		hardwareRoutes := map[neigh.HardwareRoute]uint32{}
		routesListsSet := map[bitset.TinyBitset]int{}

		routeInsertionStart := time.Now()
		totalRoutes := 0
		for prefix, routesList := range routes {
			routesListSetKey := bitset.TinyBitset{}

			if len(routesList.Routes) == 0 {
				m.log.Debugw("skip prefix with no routes", zap.Stringer("prefix", prefix))
				// FIXME add telemetry
				continue
			}

			totalRoutes += len(routesList.Routes)
			for _, route := range routesList.Routes {
				// Lookup hwaddress for the route
				entry, ok := neighbours.Lookup(route.NextHop.Unmap())
				if !ok {
					return fmt.Errorf("neighbour with %q nexthop IP address not found", route.NextHop)
				}

				if idx, ok := hardwareRoutes[entry.HardwareRoute]; ok {
					routesListSetKey.Insert(idx)
					continue
				}

				idx, err := config.RouteAdd(
					entry.HardwareRoute.SourceMAC[:],
					entry.HardwareRoute.DestinationMAC[:],
				)
				if err != nil {
					return fmt.Errorf("failed to add hardware route %q: %w", entry.HardwareRoute, err)
				}
				hardwareRoutes[entry.HardwareRoute] = uint32(idx)
				routesListSetKey.Insert(uint32(idx))
			}

			idx, ok := routesListsSet[routesListSetKey]
			if !ok {
				routeListIdx, err := config.RouteListAdd(routesListSetKey.AsSlice())
				if err != nil {
					return fmt.Errorf("failed to add routes list: %w", err)
				}
				idx = routeListIdx
			}

			if err := config.PrefixAdd(prefix, uint32(idx)); err != nil {
				return fmt.Errorf("failed to add prefix %q: %w", prefix, err)
			}
		}
		m.log.Debugw("finished inserting routes",
			zap.String("module", name),
			zap.Int("count", totalRoutes),
			zap.Uint32("numa", numaIdx),
			zap.Stringer("took", time.Since(routeInsertionStart)),
		)

		configs = append(configs, config)
	}

	for numaIdx := range numaMap.Iter() {
		agent := m.agents[numaIdx]
		config := configs[numaIdx]

		if err := agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
			return fmt.Errorf("failed to update module: %w", err)
		}

		m.log.Infow("successfully updated module",
			zap.String("name", name),
			zap.Uint32("numa", numaIdx),
		)
	}
	return nil
}

func convertRoute(prefix netip.Prefix, isBest bool, route *rib.Route) *routepb.Route {
	communities := make([]*routepb.LargeCommunity, len(route.LargeCommunities))
	for _, c := range route.LargeCommunities {
		communities = append(communities, convertLargeCommunity(c))
	}

	peer := ""
	if route.Peer.IsValid() {
		peer = route.Peer.String()
	}

	return &routepb.Route{
		Prefix:           prefix.String(),
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

func convertLargeCommunity(community rib.LargeCommunity) *routepb.LargeCommunity {
	return &routepb.LargeCommunity{
		GlobalAdministrator: community.GlobalAdministrator,
		LocalDataPart1:      community.LocalDataPart1,
		LocalDataPart2:      community.LocalDataPart2,
	}
}
