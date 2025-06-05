package route

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/netip"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/bitset"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/modules/route/internal/rib"
)

type RouteService struct {
	routepb.UnimplementedRouteServiceServer

	mu         sync.Mutex
	agents     []*ffi.Agent
	ribs       map[instanceKey]*rib.RIB
	neighCache *neigh.NexthopCache
	log        *zap.SugaredLogger
}

func NewRouteService(
	agents []*ffi.Agent,
	neighCache *neigh.NexthopCache,
	log *zap.SugaredLogger,
) *RouteService {
	return &RouteService{
		agents:     agents,
		ribs:       map[instanceKey]*rib.RIB{},
		neighCache: neighCache,
		log:        log,
	}
}

func (m *RouteService) ListConfigs(
	ctx context.Context,
	request *routepb.ListConfigsRequest,
) (*routepb.ListConfigsResponse, error) {

	response := &routepb.ListConfigsResponse{
		InstanceConfigs: make([]*routepb.InstanceConfigs, len(m.agents)),
	}
	for idx := range m.agents {
		response.InstanceConfigs[idx] = &routepb.InstanceConfigs{
			Instance: uint32(idx),
		}
	}
	for key := range maps.Keys(m.ribs) {
		instanceConfigs := response.InstanceConfigs[key.dataplaneInstance]
		instanceConfigs.Configs = append(instanceConfigs.Configs, key.name)
	}

	return response, nil
}

func (m *RouteService) ShowRoutes(
	ctx context.Context,
	request *routepb.ShowRoutesRequest,
) (*routepb.ShowRoutesResponse, error) {

	name, instances, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	holder, ok := m.ribs[instanceKey{name: name, dataplaneInstance: instances}]
	if !ok {
		return &routepb.ShowRoutesResponse{}, nil
	}
	routes := holder.DumpRoutes()

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
			response.Routes = append(response.Routes, routepb.FromRIBRoute(&r, isBest))
		}
	}

	return response, nil
}

func (m *RouteService) LookupRoute(
	ctx context.Context,
	request *routepb.LookupRouteRequest,
) (*routepb.LookupRouteResponse, error) {

	name, instances, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	addr, err := netip.ParseAddr(request.GetIpAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse IP address: %v", err)
	}

	holder, ok := m.ribs[instanceKey{name: name, dataplaneInstance: instances}]
	if !ok {
		return &routepb.LookupRouteResponse{}, nil
	}

	prefix, routes, ok := holder.LongestMatch(addr)
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
		response.Routes = append(response.Routes, routepb.FromRIBRoute(&r, isBest))
	}

	return response, nil
}

func (m *RouteService) FlushRoutes(
	ctx context.Context,
	request *routepb.FlushRoutesRequest,
) (*routepb.FlushRoutesResponse, error) {
	name, instances, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &routepb.FlushRoutesResponse{}, m.syncRouteUpdates(name, instances)
}

func (m *RouteService) InsertRoute(
	ctx context.Context,
	request *routepb.InsertRouteRequest,
) (*routepb.InsertRouteResponse, error) {
	name, instances, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	prefix, err := netip.ParsePrefix(request.GetPrefix())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
	}

	nexthopAddr, err := netip.ParseAddr(request.GetNexthopAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop address: %v", err)
	}

	holder, ok := m.ribs[instanceKey{name: name, dataplaneInstance: instances}]
	if !ok {
		holder = rib.NewRIB(m.log)
		m.ribs[instanceKey{name: name, dataplaneInstance: instances}] = holder
	}
	if err := holder.AddUnicastRoute(prefix, nexthopAddr); err != nil {
		return nil, fmt.Errorf("failed to add unicast route: %w", err)
	}

	if request.GetDoFlush() {
		return &routepb.InsertRouteResponse{}, m.syncRouteUpdates(name, instances)
	}
	return &routepb.InsertRouteResponse{}, m.syncRouteUpdates(name, instances)
}

func (m *RouteService) FeedRIB(stream grpc.ClientStreamingServer[routepb.Update, routepb.UpdateSummary]) error {
	var (
		update    *routepb.Update
		name      string
		instances uint32
		err       error
		holder    *rib.RIB
	)

	for {
		update, err = stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&routepb.UpdateSummary{})
		}
		if err != nil {
			return err
		}
		if holder == nil {
			name, instances, err = update.GetTarget().Validate(uint32(len(m.agents)))
			if err != nil {
				return status.Error(codes.InvalidArgument, err.Error())
			}
			var ok bool
			holder, ok = m.ribs[instanceKey{name: name, dataplaneInstance: instances}]
			if !ok {
				holder = rib.NewRIB(m.log)
				m.ribs[instanceKey{name: name, dataplaneInstance: instances}] = holder
			}

		}
		route, err := routepb.ToRIBRoute(update.GetRoute(), update.GetIsDelete())
		if err != nil {
			return fmt.Errorf("failed to convert proto route to RIB route: %w", err)
		}
		holder.Update(*route)
	}
}

func (m *RouteService) syncRouteUpdates(name string, dpInstance uint32) error {
	holder, ok := m.ribs[instanceKey{name: name, dataplaneInstance: dpInstance}]
	if !ok {
		m.log.Warnf("no RIB found for module '%s' on dataplane instance %d", name, dpInstance)
		return nil
	}

	routes := holder.DumpRoutes()

	// Huge mutex, but our shared memory must be protected from concurrent access.
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.updateModuleConfig(name, dpInstance, routes)
	if err != nil {
		return err
	}
	return nil
}

func (m *RouteService) updateModuleConfig(
	name string,
	inst uint32,
	routes map[netip.Prefix]rib.RoutesList,
) error {
	agent := m.agents[inst]

	config, err := NewModuleConfig(agent, name)
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	// Obtain neighbor entry with resolved hardware addresses
	neighbours := m.neighCache.View()

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
				// FIXME: add telemetry?
				m.log.Warnf("neighbour with %q nexthop IP address not found, skip", route.NextHop)
				continue
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

		if routesListSetKey.Count() == 0 {
			continue
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
		zap.Uint32("inst", inst),
		zap.Stringer("took", time.Since(routeInsertionStart)),
	)

	if err := agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %w", err)
	}

	m.log.Infow("successfully updated module",
		zap.String("name", name),
		zap.Uint32("inst", inst),
	)
	return nil
}
