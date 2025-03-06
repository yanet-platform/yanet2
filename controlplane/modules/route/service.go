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
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/rib"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/routepb"
)

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

	// Empty means all NUMA nodes.
	if len(numaIndices) == 0 {
		for idx := range m.agents {
			numaIndices = append(numaIndices, uint32(idx))
		}
	}

	configs := make([]*ModuleConfig, 0, len(numaIndices))

	// Huge mutex, but our shared memory must be protected from concurrent
	// access.
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, numaIdx := range numaIndices {
		agent := m.agents[numaIdx]

		config, err := NewModuleConfig(agent, name)
		if err != nil {
			return nil, fmt.Errorf("failed to create %q module config: %w", name, err)
		}

		if err := m.rib.AddUnicastRoute(prefix, nexthopAddr); err != nil {
			return nil, fmt.Errorf("failed to add unicast route: %w", err)
		}

		routes := m.rib.DumpRoutes()

		hardwareRoutes := map[rib.HardwareRoute]uint32{}
		routesLists := map[bitset.TinyBitset]uint32{}
		for prefix, routeList := range routes {
			routesList := bitset.TinyBitset{}

			for hardwareRoute := range routeList.Routes {
				if idx, ok := hardwareRoutes[hardwareRoute]; ok {
					routesList.Insert(idx)
					continue
				}

				idx, err := config.RouteAdd(hardwareRoute.SourceMAC[:], hardwareRoute.DestinationMAC[:])
				if err != nil {
					return nil, fmt.Errorf("failed to add hardware route %q: %w", hardwareRoute, err)
				}
				hardwareRoutes[hardwareRoute] = uint32(idx)
				routesList.Insert(uint32(idx))
			}

			idx, ok := routesLists[routesList]
			if !ok {
				routeListIdx, err := config.RouteListAdd(routesList.AsSlice())
				if err != nil {
					return nil, fmt.Errorf("failed to add routes list: %w", err)
				}
				idx = uint32(routeListIdx)
			}

			if err := config.PrefixAdd(prefix, idx); err != nil {
				return nil, fmt.Errorf("failed to add prefix %q: %w", prefix, err)
			}
		}

		configs = append(configs, config)
	}

	for _, numaIdx := range numaIndices {
		agent := m.agents[numaIdx]
		config := configs[numaIdx]

		if err := agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
			return nil, fmt.Errorf("failed to update module: %w", err)
		}

		m.log.Infow("successfully updated module",
			zap.String("name", name),
			zap.Uint32("numa", numaIdx),
		)
	}

	return &routepb.InsertRouteResponse{}, nil
}
