package route

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/bitset"
	"github.com/yanet-platform/yanet2/controlplane/internal/ffi"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/bird"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/rib"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/routepb"
)

var _ bird.RIBUpdater = (*RouteService)(nil)

type RouteService struct {
	routepb.UnimplementedRouteServer

	mu     sync.Mutex
	agents []*ffi.Agent
	rib    *rib.RIB
	log    *zap.SugaredLogger
}

func NewRouteService(agents []*ffi.Agent, rib *rib.RIB, log *zap.SugaredLogger) *RouteService {
	return &RouteService{
		agents: agents,
		rib:    rib,
		log:    log,
	}
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

	numaIndices := request.GetNuma()
	slices.Sort(numaIndices)
	numaIndices = slices.Compact(numaIndices)
	if !slices.Equal(numaIndices, request.GetNuma()) {
		return nil, status.Error(codes.InvalidArgument, "repeated NUMA indices is duplicated")
	}
	if len(numaIndices) > 0 && int(numaIndices[len(numaIndices)-1]) >= len(m.agents) {
		return nil, status.Error(codes.InvalidArgument, "NUMA indices are out of range")
	}

	if err := m.rib.AddUnicastRoute(prefix, nexthopAddr); err != nil {
		return nil, fmt.Errorf("failed to add unicast route: %w", err)
	}

	return &routepb.InsertRouteResponse{}, m.syncRouteUpdates(name, numaIndices)
}

func (m *RouteService) BulkUpdate(routes []*rib.Route) error {
	m.log.Debugw("apply bulk update", zap.Int("size", len(routes)))
	m.rib.BulkUpdate(routes)
	// TODO: notification about rib update
	return nil
}

func (m *RouteService) syncRouteUpdates(name string, numaIndices []uint32) error {
	// Empty means all NUMA nodes.
	if len(numaIndices) == 0 {
		for idx := range m.agents {
			numaIndices = append(numaIndices, uint32(idx))
		}
	}

	routes := m.rib.DumpRoutes()

	// Huge mutex, but our shared memory must be protected from concurrent access.
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateModuleConfigs(name, numaIndices, routes)
}

func (m *RouteService) updateModuleConfigs(
	name string,
	numaIndices []uint32,
	routes map[netip.Prefix]rib.RoutesList,
) error {
	configs := make([]*ModuleConfig, 0, len(numaIndices))

	for _, numaIdx := range numaIndices {
		agent := m.agents[numaIdx]

		config, err := NewModuleConfig(agent, name)
		if err != nil {
			return fmt.Errorf("failed to create %q module config: %w", name, err)
		}

		// Obtain neighbor entry with resolved hardware addresses
		neighbours := m.rib.NeighboursView()

		hardwareRoutes := map[neigh.HardwareRoute]uint32{}
		routesListsSet := map[bitset.TinyBitset]int{}
		for prefix, routesList := range routes {
			routesListSetKey := bitset.TinyBitset{}

			for _, route := range routesList.Routes {
				if route == nil {
					m.log.Debugw("skip prefix with no routes", zap.Stringer("prefix", prefix))
					// FIXME add telemetry
					continue
				}

				// Lookup hwaddress for the route
				entry, ok := neighbours.Lookup(route.NextHop)
				if !ok {
					return fmt.Errorf("neighbour with %q nexthop IP address not found", route.NextHop)
				}

				m.log.Debugw("found neighbour with resolved hardware addresses",
					zap.Stringer("nexthop_addr", route.NextHop),
				)

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

		configs = append(configs, config)
	}

	for _, numaIdx := range numaIndices {
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
