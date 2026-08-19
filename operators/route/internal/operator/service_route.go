package operator

import (
	"context"
	"io"
	"maps"
	"net/netip"
	"slices"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// RouteService implements the operator-owned RouteService surface.
//
// Mutation RPCs update the RIB held in this process and wake the
// reconcile loop via onChanged. The reconcile loop snapshots state and
// pushes the rebuilt FIB to the gateways through the actuator.
type RouteService struct {
	operatorpb.UnimplementedRouteServiceServer

	ribs       *RIBStore
	neighTable *neigh.NeighTable

	ribTTL            time.Duration
	quitCh            chan bool
	onChanged         func()
	onRIBSessionStart func(name string, sessionID uint64)
	onRIBUpdate       func(n int)
	onRIBSessionEnd   func(name string, sessionID uint64)

	// configuredModules holds the module config names the operator itself
	// manages.
	//
	// It is built once in NewRouteService and never mutated afterwards, so
	// reads need no lock.
	configuredModules map[string]struct{}

	log *zap.Logger
}

// NewRouteService constructs a RouteService bound to the supplied
// neighbour table.
func NewRouteService(
	neighTable *neigh.NeighTable,
	options ...RouteServiceOption,
) *RouteService {
	opts := newRouteServiceOptions()
	for _, o := range options {
		o(opts)
	}

	configuredModules := make(map[string]struct{}, len(opts.ConfiguredModules))
	for _, name := range opts.ConfiguredModules {
		configuredModules[name] = struct{}{}
	}

	return &RouteService{
		ribs:              opts.RIBs,
		neighTable:        neighTable,
		ribTTL:            opts.RIBTTL,
		quitCh:            make(chan bool),
		onChanged:         opts.OnChanged,
		onRIBSessionStart: opts.OnRIBSessionStart,
		onRIBUpdate:       opts.OnRIBUpdate,
		onRIBSessionEnd:   opts.OnRIBSessionEnd,
		configuredModules: configuredModules,
		log:               opts.Log,
	}
}

// Close releases resources owned by the service. It is safe to call
// concurrently with in-flight RPCs.
func (m *RouteService) Close() error {
	close(m.quitCh)
	return nil
}

// Configs returns the config names the operator can answer reads for.
//
// The result is the union of RIB-backed config names and configured
// module names, deduplicated and sorted for a deterministic listing.
func (m *RouteService) Configs() []string {
	seen := map[string]struct{}{}
	for _, name := range m.ribs.Configs() {
		seen[name] = struct{}{}
	}
	maps.Copy(seen, m.configuredModules)

	return slices.Sorted(maps.Keys(seen))
}

// isConfigured reports whether name is a module config the operator itself
// manages, regardless of whether a RIB has been created for it yet.
func (m *RouteService) isConfigured(name string) bool {
	_, ok := m.configuredModules[name]
	return ok
}

// ListConfigs returns the config names the operator can answer reads for.
//
// See Configs for what that set contains.
func (m *RouteService) ListConfigs(
	ctx context.Context,
	req *operatorpb.ListConfigsRequest,
) (*operatorpb.ListConfigsResponse, error) {
	return &operatorpb.ListConfigsResponse{
		Configs: m.Configs(),
	}, nil
}

// Snapshot returns a snapshot of all RIBs keyed by config name.
func (m *RouteService) Snapshot() map[string]*rib.RIB {
	return m.ribs.Snapshot()
}

func (m *RouteService) ShowRoutes(
	ctx context.Context,
	req *operatorpb.ShowRoutesRequest,
) (*operatorpb.ShowRoutesResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	holder, ok := m.getRib(name)
	if !ok {
		if m.isConfigured(name) {
			return &operatorpb.ShowRoutesResponse{}, nil
		}
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	ribDump := holder.DumpRoutes()

	response := &operatorpb.ShowRoutesResponse{}

	for prefixLen := range ribDump {
		for prefix, routesList := range ribDump[prefixLen] {
			if len(routesList.Routes) == 0 {
				continue
			}

			if req.GetIpv4Only() && !prefix.Addr().Is4() {
				continue
			}
			if req.GetIpv6Only() && !prefix.Addr().Is6() {
				continue
			}

			bestMask := routesList.BestPerSourceMask()
			for idx, r := range routesList.Routes {
				route, err := operatorpb.FromRIBRoute(&r, bestMask[idx])
				if err != nil {
					return nil, status.Errorf(codes.Internal, "failed to convert route: %v", err)
				}
				response.Routes = append(response.Routes, route)
			}
		}
	}

	return response, nil
}

func (m *RouteService) LookupRoute(
	ctx context.Context,
	req *operatorpb.LookupRouteRequest,
) (*operatorpb.LookupRouteResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	addr, err := req.GetIpAddr().ToAddr()
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid ip_addr (bytes=%x): %v", req.GetIpAddr().GetAddr(), err)
	}

	holder, ok := m.getRib(name)
	if !ok {
		if m.isConfigured(name) {
			return &operatorpb.LookupRouteResponse{}, nil
		}
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	prefix, routes, ok := holder.LongestMatch(addr)
	if !ok {
		return &operatorpb.LookupRouteResponse{}, nil
	}

	matched, err := commonpb.NewContiguousIPNetworkFromPrefix(prefix)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert matched prefix %q: %v", prefix, err)
	}

	response := &operatorpb.LookupRouteResponse{
		Prefix: matched,
		Routes: make([]*operatorpb.Route, 0, len(routes.Routes)),
	}

	bestMask := routes.BestPerSourceMask()
	for idx, r := range routes.Routes {
		route, err := operatorpb.FromRIBRoute(&r, bestMask[idx])
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to convert route: %v", err)
		}
		response.Routes = append(response.Routes, route)
	}

	return response, nil
}

func (m *RouteService) InsertRoute(
	ctx context.Context,
	req *operatorpb.InsertRouteRequest,
) (*operatorpb.InsertRouteResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	prefix, err := req.GetPrefix().ToPrefix()
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid prefix: %v", err)
	}

	addrs := req.GetNexthopAddrs()
	if len(addrs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one nexthop address is required")
	}

	nexthops := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		nexthop, parseErr := a.ToAddr()
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid nexthop_addrs entry (bytes=%x): %v", a.GetAddr(), parseErr)
		}
		nexthops = append(nexthops, nexthop)
	}

	sourceID := req.RouteSourceID()

	// Non-static sources use peer identity to distinguish routes: the unary
	// InsertRoute API carries no peer, so consecutive AddUnicastRoute calls
	// for the same (prefix, source) would silently replace one another.
	// Reject the ambiguous case early rather than keeping only the last nexthop.
	if sourceID != rib.RouteSourceStatic && len(nexthops) > 1 {
		return nil, status.Error(codes.InvalidArgument, "multiple nexthops are only supported for static routes")
	}

	holder := m.getOrCreateRib(name)

	for _, nexthopAddr := range nexthops {
		if err := holder.AddUnicastRoute(prefix, nexthopAddr, sourceID); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to add unicast route: %v", err)
		}
	}

	// Wake the reconcile loop only when the caller explicitly asks for a
	// flush — otherwise the RIB mutation is buffered until a later flush.
	if req.GetDoFlush() {
		m.onChanged()
	}

	return &operatorpb.InsertRouteResponse{}, nil
}

func (m *RouteService) DeleteRoute(
	ctx context.Context,
	req *operatorpb.DeleteRouteRequest,
) (*operatorpb.DeleteRouteResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	prefix, err := req.GetPrefix().ToPrefix()
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid prefix: %v", err)
	}

	addrs := req.GetNexthopAddrs()
	if len(addrs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one nexthop address is required")
	}

	nexthops := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		nexthop, parseErr := a.ToAddr()
		if parseErr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid nexthop_addrs entry (bytes=%x): %v", a.GetAddr(), parseErr)
		}
		nexthops = append(nexthops, nexthop)
	}

	sourceID := req.RouteSourceID()
	holder, ok := m.getRib(name)
	if !ok {
		return &operatorpb.DeleteRouteResponse{}, nil
	}

	for _, nexthopAddr := range nexthops {
		if err := holder.RemoveUnicastRoute(prefix, nexthopAddr, sourceID); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to remove unicast route: %v", err)
		}
	}

	// Wake the reconcile loop only when the caller explicitly asks for a
	// flush — otherwise the RIB mutation is buffered until a later flush.
	if req.GetDoFlush() {
		m.onChanged()
	}

	return &operatorpb.DeleteRouteResponse{}, nil
}

func (m *RouteService) FlushRoutes(
	ctx context.Context,
	req *operatorpb.FlushRoutesRequest,
) (*operatorpb.FlushRoutesResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}
	if _, ok := m.getRib(name); !ok {
		return &operatorpb.FlushRoutesResponse{}, nil
	}

	m.onChanged()

	return &operatorpb.FlushRoutesResponse{}, nil
}

// FeedRIB receives a stream of route updates and applies them to the
// matching RIB. Session semantics mirror the legacy route-module
// implementation: a new stream supersedes any prior session for the
// same RIB and stale routes are cleaned up after RIBTTL.
func (m *RouteService) FeedRIB(stream operatorpb.RouteService_FeedRIBServer) error {
	var (
		update     *operatorpb.Update
		name       string
		err        error
		ribRef     *rib.RIB
		sessionID  uint64
		terminated *atomic.Bool
	)
	for {
		update, err = stream.Recv()
		if err == io.EOF {
			err = stream.SendAndClose(&operatorpb.UpdateSummary{})
			break
		}
		if err != nil {
			break
		}

		if ribRef == nil {
			name = update.GetName()
			if name == "" {
				err = status.Error(codes.InvalidArgument, "module config name is required")
				break
			}
			ribRef = m.getOrCreateRib(name)
			sessionID, terminated = ribRef.NewSession()
			m.log.Info("started FeedRIB session",
				zap.Uint64("session_id", sessionID),
				zap.String("name", name),
			)
			m.onRIBSessionStart(name, sessionID)
		}

		if terminated.Load() {
			m.log.Warn("FeedRIB session terminated by a newer session",
				zap.Uint64("session_id", sessionID),
				zap.String("name", name),
			)
			err = stream.SendAndClose(&operatorpb.UpdateSummary{})
			break
		}
		if update.GetRoute() == nil {
			m.log.Info("flushed routes due to FeedRIB flush event",
				zap.Uint64("session_id", sessionID),
				zap.String("name", name),
			)
			m.onChanged()
			continue
		}

		route, convertErr := operatorpb.ToRIBRoute(update.GetRoute(), update.GetIsDelete())
		if convertErr != nil {
			m.log.Error("failed to convert proto route to RIB route",
				zap.Uint64("session_id", sessionID),
				zap.Error(convertErr),
			)
			continue
		}
		route.SessionID = sessionID
		ribRef.Update(*route)
		m.onRIBUpdate(1)
	}

	if ribRef != nil {
		m.log.Info("FeedRIB session ended; scheduling cleanup",
			zap.Uint64("session_id", sessionID),
			zap.String("name", name),
			zap.Duration("ttl", m.ribTTL),
		)
		m.onRIBSessionEnd(name, sessionID)
		go ribRef.CleanupTask(sessionID, m.quitCh, m.ribTTL)
		m.onChanged()
	}

	return err
}

func (m *RouteService) getRib(name string) (*rib.RIB, bool) {
	return m.ribs.Get(name)
}

func (m *RouteService) getOrCreateRib(name string) *rib.RIB {
	return m.ribs.GetOrCreate(name)
}
