package route

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/bitset"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/modules/route/internal/rib"
)

type RouteService struct {
	routepb.UnimplementedRouteServiceServer

	// shmLock serializes shared-memory mutations and protects the ffiModules
	// map.
	shmLock sync.RWMutex
	agent   *ffi.Agent
	// ribsLock protects the ribs map only.
	ribsLock   sync.RWMutex
	ribs       map[string]*rib.RIB
	ffiModules map[string]*ModuleConfig
	neighTable *neigh.NeighTable

	ribTTL time.Duration
	quitCh chan bool

	log *zap.SugaredLogger
}

func NewRouteService(
	agent *ffi.Agent,
	neighTable *neigh.NeighTable,
	ribTTL time.Duration,
	log *zap.SugaredLogger,
) *RouteService {
	return &RouteService{
		agent:      agent,
		ribs:       map[string]*rib.RIB{},
		ffiModules: map[string]*ModuleConfig{},
		neighTable: neighTable,
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
		Configs: []string{},
	}

	m.ribsLock.RLock()
	for key := range m.ribs {
		response.Configs = append(response.Configs, key)
	}
	m.ribsLock.RUnlock()

	return response, nil
}

func (m *RouteService) ShowRoutes(
	ctx context.Context,
	request *routepb.ShowRoutesRequest,
) (*routepb.ShowRoutesResponse, error) {

	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	holder, ok := m.getRib(name)
	if !ok {
		return &routepb.ShowRoutesResponse{}, nil
	}
	ribDump := holder.DumpRoutes()

	response := &routepb.ShowRoutesResponse{}

	for prefixLen := range ribDump {
		for prefix, routesList := range ribDump[prefixLen] {
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
	}

	return response, nil
}

func (m *RouteService) LookupRoute(
	ctx context.Context,
	request *routepb.LookupRouteRequest,
) (*routepb.LookupRouteResponse, error) {

	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	addr, err := netip.ParseAddr(request.GetIpAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse IP address: %v", err)
	}

	holder, ok := m.getRib(name)
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

func (m *RouteService) ShowFIB(
	ctx context.Context,
	request *routepb.ShowFIBRequest,
) (*routepb.ShowFIBResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.shmLock.RLock()
	ffiModule, ok := m.ffiModules[name]
	if !ok {
		m.shmLock.RUnlock()
		return &routepb.ShowFIBResponse{}, nil
	}

	// Hold RLock for the entire DumpFIB call so that a concurrent Free under
	// shmMu.Lock cannot release the underlying shared memory.
	entries, err := ffiModule.DumpFIB()
	m.shmLock.RUnlock()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to dump FIB: %v", err)
	}

	response := &routepb.ShowFIBResponse{}

	for _, e := range entries {
		if request.GetIpv4Only() && e.AddressFamily != 4 {
			continue
		}
		if request.GetIpv6Only() && e.AddressFamily != 6 {
			continue
		}

		prefix := formatPrefixRange(e.AddressFamily, e.PrefixFrom, e.PrefixTo)

		nexthops := make([]*routepb.FIBNexthop, len(e.Nexthops))
		for i, nh := range e.Nexthops {
			nexthops[i] = &routepb.FIBNexthop{
				DstMac: nh.DstMAC.String(),
				SrcMac: nh.SrcMAC.String(),
				Device: nh.Device,
			}
		}

		response.Entries = append(response.Entries, &routepb.FIBEntry{
			Prefix:   prefix,
			Nexthops: nexthops,
		})
	}

	return response, nil
}

// formatPrefixRange converts an address range to a human-readable string.
//
// If the range corresponds to a single CIDR prefix, it returns CIDR notation;
// otherwise "from-to" range notation.
func formatPrefixRange(af uint8, from netip.Addr, to netip.Addr) string {
	if prefix, ok := rangeToCIDR(af, from, to); ok {
		return prefix.String()
	}

	return from.String() + "-" + to.String()
}

// rangeToCIDR attempts to convert an address range to a CIDR prefix.
//
// Returns false if the range does not correspond to a single prefix.
func rangeToCIDR(af uint8, from netip.Addr, to netip.Addr) (netip.Prefix, bool) {
	var bits int
	if af == 4 {
		bits = 32
	} else {
		bits = 128
	}

	fromBytes := from.As16()
	toBytes := to.As16()

	// For IPv4, the relevant bytes are at offset 12..15 in As16().
	offset := 0
	if af == 4 {
		offset = 12
	}
	byteLen := bits / 8

	// Find the prefix length by XORing from and to, then counting leading
	// zeros in the XOR result.
	prefixLen := 0
	for i := range byteLen {
		xor := fromBytes[offset+i] ^ toBytes[offset+i]
		if xor == 0 {
			prefixLen += 8
			continue
		}
		// Count leading zeros in this byte.
		for bit := 7; bit >= 0; bit-- {
			if xor&(1<<bit) != 0 {
				break
			}
			prefixLen++
		}
		break
	}

	// Verify: from must have all host bits zero, to must have all host bits
	// one.
	prefix, err := from.Prefix(prefixLen)
	if err != nil {
		return netip.Prefix{}, false
	}
	if prefix.Addr() != from {
		return netip.Prefix{}, false
	}

	// Verify the "to" address matches the broadcast for this prefix.
	expectedTo := xnetip.LastAddr(prefix)
	if expectedTo != to {
		return netip.Prefix{}, false
	}

	return prefix, true
}

func (m *RouteService) FlushRoutes(
	ctx context.Context,
	request *routepb.FlushRoutesRequest,
) (*routepb.FlushRoutesResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}
	ribRef, ok := m.getRib(name)
	if !ok {
		return &routepb.FlushRoutesResponse{}, nil
	}

	if err := m.syncRouteUpdates(ribRef, name); err != nil {
		return nil, fmt.Errorf("failed to sync route updates: %w", err)
	}

	return &routepb.FlushRoutesResponse{}, nil
}

func (m *RouteService) InsertRoute(
	ctx context.Context,
	request *routepb.InsertRouteRequest,
) (*routepb.InsertRouteResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	prefix, err := netip.ParsePrefix(request.GetPrefix())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix %q: %v", request.GetPrefix(), err)
	}

	nexthopAddr, err := netip.ParseAddr(request.GetNexthopAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop address %q: %v", request.GetNexthopAddr(), err)
	}

	sourceID := request.RouteSourceID()

	holder := m.getOrCreateRib(name)

	if err := holder.AddUnicastRoute(prefix, nexthopAddr, sourceID); err != nil {
		return nil, fmt.Errorf("failed to add unicast route: %w", err)
	}

	if request.GetDoFlush() {
		if err := m.syncRouteUpdates(holder, name); err != nil {
			return nil, fmt.Errorf("failed to sync route updates: %w", err)
		}
	}

	return &routepb.InsertRouteResponse{}, nil
}

func (m *RouteService) DeleteRoute(
	ctx context.Context,
	request *routepb.DeleteRouteRequest,
) (*routepb.DeleteRouteResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	prefix, err := netip.ParsePrefix(request.GetPrefix())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
	}

	nexthopAddr, err := netip.ParseAddr(request.GetNexthopAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop address: %v", err)
	}

	sourceID := request.RouteSourceID()

	holder, ok := m.getRib(name)
	if !ok {
		return &routepb.DeleteRouteResponse{}, nil
	}

	if err := holder.RemoveUnicastRoute(prefix, nexthopAddr, sourceID); err != nil {
		return nil, fmt.Errorf("failed to remove unicast route: %w", err)
	}

	if request.GetDoFlush() {
		if err := m.syncRouteUpdates(holder, name); err != nil {
			return nil, fmt.Errorf("failed to sync route deletions: %w", err)
		}
	}

	return &routepb.DeleteRouteResponse{}, nil
}

func (m *RouteService) DeleteConfig(
	ctx context.Context,
	request *routepb.DeleteConfigRequest,
) (*routepb.DeleteConfigResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Lock order: shmLock -> ribsLock.
	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	// Delete the module config from the data plane if it exists.
	ffiModule, hasFFIModule := m.ffiModules[name]
	if hasFFIModule {
		if err := m.agent.DeleteModuleConfig(name); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to delete module config %q: %v", name, err)
		}
		ffiModule.Free()
		delete(m.ffiModules, name)
	}

	// Remove the RIB from the map.
	m.ribsLock.Lock()
	if _, ok := m.ribs[name]; !ok {
		m.ribsLock.Unlock()
		return &routepb.DeleteConfigResponse{}, nil
	}
	delete(m.ribs, name)
	m.ribsLock.Unlock()

	return &routepb.DeleteConfigResponse{}, nil
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
			name = update.GetName()
			if name == "" {
				err = status.Error(codes.InvalidArgument, "module config name is required")
				break // Invalid target, cannot proceed.
			}
			ribRef = m.getOrCreateRib(name)
			// NewSession() increments RIB's session counter and returns the new ID.
			// It also sets the termination flag for the *previous* session's stream.
			sessionId, terminated = ribRef.NewSession()
			m.log.Infof("new FeedRIB session %d started for %s", sessionId, name)
		}

		// Check if this session has been superseded by a newer one.
		if terminated.Load() {
			m.log.Warnf("FeedRIB session %d for %s terminated by a newer session", sessionId, name)
			err = stream.SendAndClose(&routepb.UpdateSummary{}) // Gracefully close our side.
			break
		}
		if update.GetRoute() == nil { // flush event
			m.log.Infof("sync routes in session %d for %s due to flush event in FeedRIB stream", sessionId, name)
			err = m.syncRouteUpdates(ribRef, name)
			if err != nil {
				break
			}
		} else {
			route, convertErr := routepb.ToRIBRoute(update.GetRoute(), update.GetIsDelete())
			if convertErr != nil {
				m.log.Errorf("failed to convert proto route to RIB route for session %d: %v. Update: %+v", sessionId, convertErr, update)
				continue // Skip this invalid route update.
			}
			route.SessionID = sessionId // Tag route with current session ID.
			ribRef.Update(*route)
		}

	}

	// If a RIB was established for this stream, schedule cleanup for its session.
	// This runs regardless of whether the stream ended cleanly or with an error.
	if ribRef != nil {
		m.log.Infof("FeedRIB session %d for %s ended. Scheduling cleanup after %s", sessionId, name, m.ribTTL)
		// CleanupTask will remove routes from this sessionID (and older BIRD ones) after ribTTL.
		go ribRef.CleanupTask(sessionId, m.quitCh, m.ribTTL)
	}

	// err will be nil on clean EOF, or the stream error otherwise.
	return err
}

func (m *RouteService) getRib(name string) (*rib.RIB, bool) {
	m.ribsLock.RLock()
	defer m.ribsLock.RUnlock()
	rib, ok := m.ribs[name]
	return rib, ok
}

func (m *RouteService) getOrCreateRib(name string) *rib.RIB {
	m.ribsLock.Lock()
	defer m.ribsLock.Unlock()

	ribRef, ok := m.ribs[name]
	if !ok {
		m.log.Infow("creating new RIB",
			zap.String("name", name),
		)
		ribRef = rib.NewRIB(m.log)
		m.ribs[name] = ribRef
	}
	return ribRef
}

func (m *RouteService) syncRouteUpdates(ribRef *rib.RIB, name string) error {
	ribDump := ribRef.DumpRoutes()

	// Huge mutex, but our shared memory must be protected from concurrent access.
	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	err := m.updateModuleConfig(name, ribDump)
	if err != nil {
		m.log.Errorw("syncRouteUpdates: failed to update module config",
			zap.Error(err),
			zap.String("name", name),
		)
		return err
	}
	return nil
}

func (m *RouteService) updateModuleConfig(
	name string,
	ribDump rib.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList],
) error {
	config, err := NewModuleConfig(m.agent, name)
	if err != nil {
		m.log.Errorw("updateModuleConfig: failed to create module config",
			zap.Error(err),
			zap.String("name", name),
		)
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	// Obtain neighbour entry with resolved hardware addresses
	neighbours := m.neighTable.View()

	// Statistics for summary logging
	var stats struct {
		totalPrefixes       int
		totalRoutes         int
		skippedPrefixes     int
		neighbourNotFound   int
		hardwareRoutesAdded int
		prefixesAdded       int
	}

	hardwareRoutes := map[neigh.HardwareRoute]uint32{}
	routesListsSet := map[bitset.TinyBitset]int{}

	routeInsertionStart := time.Now()

	for prefixLen := range ribDump {
		for prefix, routesList := range ribDump[prefixLen] {
			stats.totalPrefixes++
			routesListSetKey := bitset.TinyBitset{}

			if len(routesList.Routes) == 0 {
				stats.skippedPrefixes++
				continue
			}

			stats.totalRoutes += len(routesList.Routes)

			for _, route := range routesList.Routes {
				// Lookup hwaddress for the route
				entry, ok := neighbours.Lookup(route.NextHop.Unmap())
				if !ok {
					m.log.Warnw("updateModuleConfig: neighbour not found for nexthop",
						zap.Stringer("nexthop", route.NextHop),
						zap.Stringer("prefix", prefix),
						zap.String("name", name),
					)
					stats.neighbourNotFound++
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
					m.log.Errorw("updateModuleConfig: failed to add hardware route",
						zap.Error(err),
						zap.Stringer("hardware_route", entry.HardwareRoute),
						zap.Stringer("prefix", prefix),
						zap.String("name", name),
					)
					return fmt.Errorf("failed to add hardware route %v for prefix %s: %w", entry.HardwareRoute, prefix, err)
				}
				stats.hardwareRoutesAdded++
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
					m.log.Errorw("updateModuleConfig: failed to add route list",
						zap.Error(err),
						zap.Uint32s("route_indices", routesListSetKey.AsSlice()),
						zap.Stringer("prefix", prefix),
						zap.String("name", name),
					)
					return fmt.Errorf("failed to add routes list: %w", err)
				}
				idx = routeListIdx
				routesListsSet[routesListSetKey] = idx
			}

			if err := config.PrefixAdd(prefix, uint32(idx)); err != nil {
				m.log.Errorw("updateModuleConfig: failed to add prefix",
					zap.Error(err),
					zap.Stringer("prefix", prefix),
					zap.Int("route_list_index", idx),
					zap.String("name", name),
				)
				return fmt.Errorf("failed to add prefix %q: %w", prefix, err)
			}
			stats.prefixesAdded++
		}
	}

	m.log.Infow("updateModuleConfig: finished processing routes",
		zap.String("module", name),
		zap.Int("total_prefixes", stats.totalPrefixes),
		zap.Int("total_routes", stats.totalRoutes),
		zap.Int("skipped_prefixes", stats.skippedPrefixes),
		zap.Int("neighbour_not_found", stats.neighbourNotFound),
		zap.Int("hardware_routes_added", stats.hardwareRoutesAdded),
		zap.Int("prefixes_added", stats.prefixesAdded),
		zap.Duration("processing_duration", time.Since(routeInsertionStart)),
	)

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		m.log.Errorw("updateModuleConfig: failed to update modules via FFI",
			zap.Error(err),
			zap.String("name", name),
		)
		return fmt.Errorf("failed to update module: %w", err)
	}

	// Swap the FFI module and free the old one.
	//
	// The caller already holds shmLock, so both the ffiModules map and
	// the Free call are protected.
	if oldModule, exists := m.ffiModules[name]; exists {
		oldModule.Free()
	}
	m.ffiModules[name] = config

	return nil
}
