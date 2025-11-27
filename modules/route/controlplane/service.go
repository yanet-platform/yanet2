package route

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/netip"
	"sync"
	"sync/atomic"
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
	ribsLock   sync.RWMutex
	ribs       map[instanceKey]*rib.RIB
	neighCache *neigh.NexthopCache

	ribTTL time.Duration
	quitCh chan bool

	log *zap.SugaredLogger
}

func NewRouteService(
	agents []*ffi.Agent,
	neighCache *neigh.NexthopCache,
	ribTTL time.Duration,
	log *zap.SugaredLogger,
) *RouteService {
	return &RouteService{
		agents:     agents,
		ribs:       map[instanceKey]*rib.RIB{},
		neighCache: neighCache,
		ribTTL:     ribTTL,
		quitCh:     make(chan bool),
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
	m.ribsLock.RLock()
	for key := range maps.Keys(m.ribs) {
		instanceConfigs := response.InstanceConfigs[key.dataplaneInstance]
		instanceConfigs.Configs = append(instanceConfigs.Configs, key.name)
	}
	m.ribsLock.RUnlock()

	return response, nil
}

func (m *RouteService) ShowRoutes(
	ctx context.Context,
	request *routepb.ShowRoutesRequest,
) (*routepb.ShowRoutesResponse, error) {

	name, instance, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	holder, ok := m.getRib(name, instance)
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

	name, instance, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	addr, err := netip.ParseAddr(request.GetIpAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse IP address: %v", err)
	}

	holder, ok := m.getRib(name, instance)
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
	name, instance, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	ribRef, ok := m.getRib(name, instance)
	if !ok {
		m.log.Warnf("no RIB found for module '%s' on dataplane instance %d", name, instance)
		return &routepb.FlushRoutesResponse{}, nil
	}

	return &routepb.FlushRoutesResponse{}, m.syncRouteUpdates(ribRef, name, instance)
}

func (m *RouteService) InsertRoute(
	ctx context.Context,
	request *routepb.InsertRouteRequest,
) (*routepb.InsertRouteResponse, error) {
	name, instance, err := request.GetTarget().Validate(uint32(len(m.agents)))
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

	holder := m.getOrCreateRib(name, instance)

	if err := holder.AddUnicastRoute(prefix, nexthopAddr); err != nil {
		return nil, fmt.Errorf("failed to add unicast route: %w", err)
	}

	if request.GetDoFlush() {
		return &routepb.InsertRouteResponse{}, m.syncRouteUpdates(holder, name, instance)
	}
	return &routepb.InsertRouteResponse{}, nil
}

// FeedRIB receives a stream of route updates (typically from BIRD) and applies them to the
// appropriate RIB instance. It implements session management to handle stale routes:
//  1. On first update, a new session is started in the RIB. This invalidates any prior session
//     for the same RIB, signaling its stream (if active) to terminate.
//  2. Routes received are tagged with the current session ID.
//  3. If this stream is superseded by another FeedRIB call for the same RIB,
//     its `terminated` flag will be set, causing this stream to close.
//  4. When the stream ends (EOF or error), a CleanupTask is launched for the RIB
//     to remove routes from this session (and older BIRD sessions) after a TTL.
func (m *RouteService) FeedRIB(stream grpc.ClientStreamingServer[routepb.Update, routepb.UpdateSummary]) error {
	var (
		update     *routepb.Update
		name       string
		instance   uint32
		err        error
		ribRef     *rib.RIB     // Reference to the target RIB for this stream.
		sessionId  uint64       // ID for the current route import session.
		terminated *atomic.Bool // Flag to signal termination of this specific stream.
	)
	for {
		update, err = stream.Recv()
		if err == io.EOF { // Stream closed by client.
			err = stream.SendAndClose(&routepb.UpdateSummary{})
			break
		}
		if err != nil { // Other stream error.
			break
		}

		// On the first update, identify the target RIB and start a new session.
		if ribRef == nil {
			name, instance, err = update.GetTarget().Validate(uint32(len(m.agents)))
			if err != nil {
				err = status.Error(codes.InvalidArgument, err.Error())
				break // Invalid target, cannot proceed.
			}
			ribRef = m.getOrCreateRib(name, instance)
			// NewSession() increments RIB's session counter and returns the new ID.
			// It also sets the termination flag for the *previous* session's stream.
			sessionId, terminated = ribRef.NewSession()
			m.log.Infof("new FeedRIB session %d started for %s on instance %d", sessionId, name, instance)
		}

		// Check if this session has been superseded by a newer one.
		if terminated.Load() {
			m.log.Warnf("FeedRIB session %d for %s on instance %d terminated by a newer session", sessionId, name, instance)
			err = stream.SendAndClose(&routepb.UpdateSummary{}) // Gracefully close our side.
			break
		}

		route, convertErr := routepb.ToRIBRoute(update.GetRoute(), update.GetIsDelete())
		if convertErr != nil {
			m.log.Errorf("failed to convert proto route to RIB route for session %d: %v. Update: %+v", sessionId, convertErr, update)
			continue // Skip this invalid route update.
		}
		route.SessionID = sessionId // Tag route with current session ID.
		ribRef.Update(*route)
	}

	// If a RIB was established for this stream, schedule cleanup for its session.
	// This runs regardless of whether the stream ended cleanly or with an error.
	if ribRef != nil {
		m.log.Infof("FeedRIB session %d for %s on instance %d ended. Scheduling cleanup.", sessionId, name, instance)
		// CleanupTask will remove routes from this sessionID (and older BIRD ones) after ribTTL.
		go ribRef.CleanupTask(sessionId, m.quitCh, m.ribTTL)
	}

	// err will be nil on clean EOF, or the stream error otherwise.
	return err
}

func (m *RouteService) getRib(name string, instance uint32) (*rib.RIB, bool) {
	m.ribsLock.RLock()
	defer m.ribsLock.RUnlock()
	rib, ok := m.ribs[instanceKey{name: name, dataplaneInstance: instance}]
	return rib, ok
}

func (m *RouteService) getOrCreateRib(name string, instance uint32) *rib.RIB {
	m.ribsLock.Lock()
	defer m.ribsLock.Unlock()
	ribRef, ok := m.ribs[instanceKey{name: name, dataplaneInstance: instance}]
	if !ok {
		ribRef = rib.NewRIB(m.log)
		m.ribs[instanceKey{name: name, dataplaneInstance: instance}] = ribRef
	}
	return ribRef
}

func (m *RouteService) syncRouteUpdates(ribRef *rib.RIB, name string, dpInstance uint32) error {
	routes := ribRef.DumpRoutes()

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
				entry.HardwareRoute.Device,
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
