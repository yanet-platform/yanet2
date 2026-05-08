package operator

import (
	"context"
	"io"
	"net/netip"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

	ribTTL    time.Duration
	quitCh    chan bool
	onChanged func()

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

	return &RouteService{
		ribs:       opts.RIBs,
		neighTable: neighTable,
		ribTTL:     opts.RIBTTL,
		quitCh:     make(chan bool),
		onChanged:  opts.OnChanged,
		log:        opts.Log,
	}
}

// Close releases resources owned by the service. It is safe to call
// concurrently with in-flight RPCs.
func (m *RouteService) Close() error {
	close(m.quitCh)
	return nil
}

// Configs returns a snapshot of all known RIB config names.
func (m *RouteService) Configs() []string {
	return m.ribs.Configs()
}

// ListConfigs returns the names of all RIB configs known to the
// operator.
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
		return &operatorpb.ShowRoutesResponse{}, nil
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

			for idx, r := range routesList.Routes {
				isBest := idx == 0
				response.Routes = append(response.Routes, operatorpb.FromRIBRoute(&r, isBest))
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

	addr, err := netip.ParseAddr(req.GetIpAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse IP address: %v", err)
	}

	holder, ok := m.getRib(name)
	if !ok {
		return &operatorpb.LookupRouteResponse{}, nil
	}

	prefix, routes, ok := holder.LongestMatch(addr)
	if !ok {
		return &operatorpb.LookupRouteResponse{}, nil
	}

	response := &operatorpb.LookupRouteResponse{
		Prefix: prefix.String(),
		Routes: make([]*operatorpb.Route, 0, len(routes.Routes)),
	}

	for idx, r := range routes.Routes {
		isBest := idx == 0
		response.Routes = append(response.Routes, operatorpb.FromRIBRoute(&r, isBest))
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

	prefix, err := netip.ParsePrefix(req.GetPrefix())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix %q: %v", req.GetPrefix(), err)
	}

	nexthopAddr, err := netip.ParseAddr(req.GetNexthopAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop address %q: %v", req.GetNexthopAddr(), err)
	}

	sourceID := req.RouteSourceID()
	holder := m.getOrCreateRib(name)

	if err := holder.AddUnicastRoute(prefix, nexthopAddr, sourceID); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to add unicast route: %v", err)
	}

	// Wake the reconcile loop only when the caller explicitly asks for a
	// flush; otherwise the RIB mutation is buffered until a later flush.
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

	prefix, err := netip.ParsePrefix(req.GetPrefix())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
	}

	nexthopAddr, err := netip.ParseAddr(req.GetNexthopAddr())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop address: %v", err)
	}

	sourceID := req.RouteSourceID()
	holder, ok := m.getRib(name)
	if !ok {
		return &operatorpb.DeleteRouteResponse{}, nil
	}

	if err := holder.RemoveUnicastRoute(prefix, nexthopAddr, sourceID); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to remove unicast route: %v", err)
	}

	// Wake the reconcile loop only when the caller explicitly asks for a
	// flush; otherwise the RIB mutation is buffered until a later flush.
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
	}

	if ribRef != nil {
		m.log.Info("FeedRIB session ended; scheduling cleanup",
			zap.Uint64("session_id", sessionID),
			zap.String("name", name),
			zap.Duration("ttl", m.ribTTL),
		)
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
