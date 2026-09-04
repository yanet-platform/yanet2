package operator_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	sidecarpb "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/sidecarpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	routeoperator "github.com/yanet-platform/yanet2/operators/route/internal/operator"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

type recordingGateway struct {
	routepb.UnimplementedRouteServiceServer
	ynpb.UnimplementedFunctionServiceServer
	sidecarpb.UnimplementedNetlinkDataplaneServiceServer

	mu                   sync.Mutex
	staticRouteCalls     int
	staticRouteSnapshots [][][]*sidecarpb.StaticRoute
	fibRequests          []*routepb.UpdateFIBRequest
	functionGets         int
	functionUpdates      int
	staticRouteError     error
	staticRouteCommitted func()
	fibError             error
	functionError        error
}

type gatewayState struct {
	staticRouteCalls     int
	staticRouteSnapshots [][][]*sidecarpb.StaticRoute
	fibRequests          []*routepb.UpdateFIBRequest
	functionGets         int
	functionUpdates      int
}

// UpdateStaticRoutes records every complete client stream as one snapshot.
func (m *recordingGateway) UpdateStaticRoutes(
	stream grpc.ClientStreamingServer[
		sidecarpb.UpdateStaticRoutesRequest,
		sidecarpb.UpdateStaticRoutesResponse,
	],
) error {
	m.mu.Lock()
	m.staticRouteCalls++
	m.mu.Unlock()

	chunks := [][]*sidecarpb.StaticRoute{}
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			m.mu.Lock()
			m.staticRouteSnapshots = append(m.staticRouteSnapshots, chunks)
			configuredError := m.staticRouteError
			committed := m.staticRouteCommitted
			m.mu.Unlock()

			if configuredError != nil {
				return configuredError
			}
			if committed != nil {
				committed()
			}
			return stream.SendAndClose(&sidecarpb.UpdateStaticRoutesResponse{})
		}
		if err != nil {
			return err
		}

		chunk := make([]*sidecarpb.StaticRoute, len(request.GetRoutes()))
		for idx, route := range request.GetRoutes() {
			chunk[idx] = proto.Clone(route).(*sidecarpb.StaticRoute)
		}
		chunks = append(chunks, chunk)
	}
}

// UpdateFIB records each attempted route-module update.
func (m *recordingGateway) UpdateFIB(
	ctx context.Context,
	request *routepb.UpdateFIBRequest,
) (*routepb.UpdateFIBResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fibRequests = append(
		m.fibRequests,
		proto.Clone(request).(*routepb.UpdateFIBRequest),
	)
	if m.fibError != nil {
		return nil, m.fibError
	}
	return &routepb.UpdateFIBResponse{}, nil
}

// Get records function reconciliation and reports that the function is absent.
func (m *recordingGateway) Get(
	ctx context.Context,
	request *ynpb.GetFunctionRequest,
) (*ynpb.GetFunctionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.functionGets++
	return nil, status.Error(codes.NotFound, "function not found")
}

// Update records each attempted function update.
func (m *recordingGateway) Update(
	ctx context.Context,
	request *ynpb.UpdateFunctionRequest,
) (*ynpb.UpdateFunctionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.functionUpdates++
	if m.functionError != nil {
		return nil, m.functionError
	}
	return &ynpb.UpdateFunctionResponse{}, nil
}

// state returns a stable copy of all calls observed by the fake gateway.
func (m *recordingGateway) state() gatewayState {
	m.mu.Lock()
	defer m.mu.Unlock()

	return gatewayState{
		staticRouteCalls:     m.staticRouteCalls,
		staticRouteSnapshots: append([][][]*sidecarpb.StaticRoute(nil), m.staticRouteSnapshots...),
		fibRequests:          append([]*routepb.UpdateFIBRequest(nil), m.fibRequests...),
		functionGets:         m.functionGets,
		functionUpdates:      m.functionUpdates,
	}
}

// serveRecordingGateway exposes all services through one loopback connection.
func serveRecordingGateway(t *testing.T, gateway *recordingGateway) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	routepb.RegisterRouteServiceServer(server, gateway)
	ynpb.RegisterFunctionServiceServer(server, gateway)
	sidecarpb.RegisterNetlinkDataplaneServiceServer(server, gateway)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	return listener.Addr().String()
}

// newTestGatewayActuator constructs an actuator using the recording endpoint.
func newTestGatewayActuator(
	t *testing.T,
	endpoint string,
	staticRoutes []routeoperator.StaticRouteConfig,
	sidecarEnabled bool,
) routeoperator.Actuator {
	t.Helper()
	actuator := newRawTestGatewayActuator(t, "numa0", endpoint, sidecarEnabled)
	if !sidecarEnabled {
		return actuator
	}
	return routeoperator.NewNetlinkSidecarActuator(
		actuator,
		staticRoutes,
		5*time.Second,
		func() neigh.TableSnapshot { return dynamicRIBSnapshot().Neighbours },
		actuator,
	)
}

func newRawTestGatewayActuator(
	t *testing.T,
	name string,
	endpoint string,
	sidecarEnabled bool,
) *routeoperator.GatewayActuator {
	t.Helper()

	options := []routeoperator.GatewayActuatorOption{
		routeoperator.WithGatewayActuatorFunction(routeoperator.FunctionConfig{
			Name:   xcfg.MustNonEmptyString("fn:route"),
			Chain:  xcfg.MustNonEmptyString("default"),
			Weight: 1,
			Module: xcfg.MustNonEmptyString("route0"),
		}),
	}
	if sidecarEnabled {
		options = append(options, routeoperator.WithGatewayActuatorNetlinkSidecar())
	}

	actuator, err := routeoperator.NewGatewayActuator(
		commonoperator.GatewayConfig{
			Name:     name,
			Endpoint: xcfg.MustNonEmptyString(endpoint),
		},
		options...,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = actuator.Close() })
	return actuator
}

// applyGatewayActuator bounds an apply call so a broken stream fails promptly.
func applyGatewayActuator(
	t *testing.T,
	actuator routeoperator.Actuator,
	snapshot routeoperator.RouteSnapshot,
) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	return actuator.Apply(ctx, snapshot)
}

// emptyFIBSnapshot returns one module snapshot so a FIB RPC is attempted.
func emptyFIBSnapshot() routeoperator.RouteSnapshot {
	routes := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](0)
	neighbours := rcucache.NewEmptyCache[netip.Addr, neigh.NeighbourEntry]()
	return routeoperator.RouteSnapshot{
		RIBs: map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]{
			"route0": routes,
		},
		Neighbours: neigh.NewTableSnapshot(neighbours.View()),
	}
}

// dynamicRIBSnapshot returns one resolvable BIRD route for the normal FIB path.
func dynamicRIBSnapshot() routeoperator.RouteSnapshot {
	prefix := netip.MustParsePrefix("198.51.100.0/24")
	nexthop := netip.MustParseAddr("198.51.100.1")
	routes := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](1)
	routes[prefix.Bits()][prefix] = rib.RoutesList{Routes: []rib.Route{{
		Prefix:   prefix,
		NextHop:  nexthop,
		SourceID: rib.RouteSourceBird,
	}}}
	neighbours := rcucache.NewCache(map[netip.Addr]neigh.NeighbourEntry{
		nexthop: {
			HardwareRoute: neigh.HardwareRoute{Device: "dynamic0"},
		},
	})
	return routeoperator.RouteSnapshot{
		RIBs: map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]{
			"route0": routes,
		},
		Neighbours: neigh.NewTableSnapshot(neighbours.View()),
	}
}

type staticRouteRecord struct {
	prefix        string
	nexthop       string
	interfaceName string
}

// decodeStaticRouteSnapshot converts recorded protobuf chunks into comparable rows.
func decodeStaticRouteSnapshot(
	t *testing.T,
	chunks [][]*sidecarpb.StaticRoute,
) []staticRouteRecord {
	t.Helper()

	routes := []staticRouteRecord{}
	for _, chunk := range chunks {
		for _, route := range chunk {
			prefix, err := route.GetPrefix().ToPrefix()
			require.NoError(t, err)
			nexthop, err := route.GetNexthop().ToAddr()
			require.NoError(t, err)
			routes = append(routes, staticRouteRecord{
				prefix:        prefix.String(),
				nexthop:       nexthop.String(),
				interfaceName: route.GetInterface(),
			})
		}
	}
	return routes
}

// Test_GatewayActuator_Apply_DisabledSidecarMakesNoRPC verifies that omission
// of the enabled option leaves static-route publication entirely inactive.
func Test_GatewayActuator_Apply_DisabledSidecarMakesNoRPC(t *testing.T) {
	gateway := &recordingGateway{}
	endpoint := serveRecordingGateway(t, gateway)
	actuator := newTestGatewayActuator(t, endpoint, nil, false)

	require.NoError(t, applyGatewayActuator(t, actuator, routeoperator.RouteSnapshot{}))
	require.Zero(t, gateway.state().staticRouteCalls)
}

// Test_GatewayActuator_Apply_EnabledPublishesOnlyStaticConfigEveryTime verifies
// that dynamic RIB state never enters the complete configured sidecar snapshot.
func Test_GatewayActuator_Apply_EnabledPublishesOnlyStaticConfigEveryTime(t *testing.T) {
	gateway := &recordingGateway{}
	endpoint := serveRecordingGateway(t, gateway)
	actuator := newTestGatewayActuator(t, endpoint, []routeoperator.StaticRouteConfig{
		{Prefix: "192.0.2.0/24", NexthopAddr: "192.0.2.1", Interface: "static0"},
		{Prefix: "2001:db8::/64", NexthopAddr: "2001:db8::1", Interface: "static1"},
	}, true)

	snapshot := dynamicRIBSnapshot()
	require.NoError(t, applyGatewayActuator(t, actuator, snapshot))
	require.NoError(t, applyGatewayActuator(t, actuator, snapshot))

	state := gateway.state()
	require.Equal(t, 2, state.staticRouteCalls)
	require.Len(t, state.staticRouteSnapshots, 2)
	expected := []staticRouteRecord{
		{prefix: "192.0.2.0/24", nexthop: "192.0.2.1", interfaceName: "static0"},
		{prefix: "2001:db8::/64", nexthop: "2001:db8::1", interfaceName: "static1"},
	}
	for _, staticRouteSnapshot := range state.staticRouteSnapshots {
		require.Equal(t, expected, decodeStaticRouteSnapshot(t, staticRouteSnapshot))
	}
	require.Len(t, state.fibRequests, 2)
	require.Len(t, state.fibRequests[0].GetEntries(), 1)
	require.Len(t, state.fibRequests[1].GetEntries(), 1)
}

func Test_NetlinkSidecarActuator_RefreshesNeighboursAfterPublication(t *testing.T) {
	gateway := &recordingGateway{}
	endpoint := serveRecordingGateway(t, gateway)
	inner := newRawTestGatewayActuator(t, "numa0", endpoint, true)
	snapshot := dynamicRIBSnapshot()
	freshNeighbours := snapshot.Neighbours
	snapshot.Neighbours = emptyFIBSnapshot().Neighbours

	var neighboursMu sync.Mutex
	currentNeighbours := snapshot.Neighbours
	gateway.staticRouteCommitted = func() {
		neighboursMu.Lock()
		defer neighboursMu.Unlock()
		currentNeighbours = freshNeighbours
	}
	actuator := routeoperator.NewNetlinkSidecarActuator(
		inner,
		nil,
		5*time.Second,
		func() neigh.TableSnapshot {
			neighboursMu.Lock()
			defer neighboursMu.Unlock()
			return currentNeighbours
		},
		inner,
	)

	require.NoError(t, applyGatewayActuator(t, actuator, snapshot))

	state := gateway.state()
	require.Len(t, state.fibRequests, 1)
	require.Len(t, state.fibRequests[0].GetEntries(), 1)
}

// Test_GatewayActuator_Apply_MasksStaticPrefixHostBits verifies that sidecar
// publication uses the same network prefix normalization as static RIB seeding.
func Test_GatewayActuator_Apply_MasksStaticPrefixHostBits(t *testing.T) {
	gateway := &recordingGateway{}
	endpoint := serveRecordingGateway(t, gateway)
	actuator := newTestGatewayActuator(t, endpoint, []routeoperator.StaticRouteConfig{{
		Prefix:      "192.0.2.123/24",
		NexthopAddr: "192.0.2.1",
		Interface:   "static0",
	}}, true)

	require.NoError(t, applyGatewayActuator(t, actuator, routeoperator.RouteSnapshot{}))

	state := gateway.state()
	require.Len(t, state.staticRouteSnapshots, 1)
	routes := decodeStaticRouteSnapshot(t, state.staticRouteSnapshots[0])
	require.Equal(t, "192.0.2.0/24", routes[0].prefix)
}

// Test_GatewayActuator_Apply_ChunksLargeStaticSnapshot verifies that no stream
// request contains more than one thousand configured routes.
func Test_GatewayActuator_Apply_ChunksLargeStaticSnapshot(t *testing.T) {
	staticRoutes := make([]routeoperator.StaticRouteConfig, 1001)
	for idx := range staticRoutes {
		staticRoutes[idx] = routeoperator.StaticRouteConfig{
			Prefix:      fmt.Sprintf("10.%d.%d.1/32", idx/256, idx%256),
			NexthopAddr: "192.0.2.1",
			Interface:   "static0",
		}
	}
	gateway := &recordingGateway{}
	endpoint := serveRecordingGateway(t, gateway)
	actuator := newTestGatewayActuator(t, endpoint, staticRoutes, true)

	require.NoError(t, applyGatewayActuator(t, actuator, routeoperator.RouteSnapshot{}))

	state := gateway.state()
	require.Len(t, state.staticRouteSnapshots, 1)
	require.Len(t, state.staticRouteSnapshots[0], 2)
	require.Len(t, state.staticRouteSnapshots[0][0], 1000)
	require.Len(t, state.staticRouteSnapshots[0][1], 1)
}

// Test_GatewayActuator_Apply_EmptyStaticSnapshotSendsChunk verifies that an
// explicit empty request reaches the sidecar before the stream is committed.
func Test_GatewayActuator_Apply_EmptyStaticSnapshotSendsChunk(t *testing.T) {
	gateway := &recordingGateway{}
	endpoint := serveRecordingGateway(t, gateway)
	actuator := newTestGatewayActuator(t, endpoint, nil, true)

	require.NoError(t, applyGatewayActuator(t, actuator, routeoperator.RouteSnapshot{}))

	state := gateway.state()
	require.Equal(t, 1, state.staticRouteCalls)
	require.Len(t, state.staticRouteSnapshots, 1)
	require.Len(t, state.staticRouteSnapshots[0], 1)
	require.Empty(t, state.staticRouteSnapshots[0][0])
}

// Test_GatewayActuator_Apply_ConversionFailureStartsNoWork verifies that all
// configured routes convert before sidecar, FIB, or function RPC work.
func Test_GatewayActuator_Apply_ConversionFailureStartsNoStream(t *testing.T) {
	gateway := &recordingGateway{}
	endpoint := serveRecordingGateway(t, gateway)
	actuator := newTestGatewayActuator(t, endpoint, []routeoperator.StaticRouteConfig{
		{Prefix: "192.0.2.0/24", NexthopAddr: "192.0.2.1", Interface: "static0"},
		{Prefix: "not-a-prefix", NexthopAddr: "192.0.2.2", Interface: "static1"},
	}, true)

	err := applyGatewayActuator(t, actuator, emptyFIBSnapshot())
	require.ErrorContains(t, err, "static route 1: invalid prefix")

	state := gateway.state()
	require.Zero(t, state.staticRouteCalls)
	require.Empty(t, state.fibRequests)
	require.Zero(t, state.functionGets)
	require.Zero(t, state.functionUpdates)
}

// Test_GatewayActuator_Apply_SidecarFailureStopsGatewayUpdates verifies that a
// failed sidecar commit cannot leave host routes behind gateway state.
func Test_GatewayActuator_Apply_SidecarFailureStopsGatewayUpdates(t *testing.T) {
	gateway := &recordingGateway{
		staticRouteError: status.Error(codes.Internal, "sidecar failure"),
	}
	endpoint := serveRecordingGateway(t, gateway)
	actuator := newTestGatewayActuator(t, endpoint, []routeoperator.StaticRouteConfig{{
		Prefix:      "192.0.2.0/24",
		NexthopAddr: "192.0.2.1",
		Interface:   "static0",
	}}, true)

	err := applyGatewayActuator(t, actuator, emptyFIBSnapshot())
	require.ErrorContains(t, err, "sidecar failure")

	state := gateway.state()
	require.Equal(t, 1, state.staticRouteCalls)
	require.Empty(t, state.fibRequests)
	require.Zero(t, state.functionGets)
	require.Zero(t, state.functionUpdates)
}

// Test_NetlinkSidecarActuator_TriesGatewaysUntilSuccess verifies that gateway
// fan-out starts once, after one ordered sidecar path commits the snapshot.
func Test_NetlinkSidecarActuator_TriesGatewaysUntilSuccess(t *testing.T) {
	firstGateway := &recordingGateway{
		staticRouteError: status.Error(codes.Unavailable, "first path unavailable"),
	}
	secondGateway := &recordingGateway{}
	first := newRawTestGatewayActuator(
		t,
		"numa0",
		serveRecordingGateway(t, firstGateway),
		true,
	)
	second := newRawTestGatewayActuator(
		t,
		"numa1",
		serveRecordingGateway(t, secondGateway),
		true,
	)
	fanOut := commonoperator.NewFanOutActuator([]routeoperator.Actuator{first, second})
	actuator := routeoperator.NewNetlinkSidecarActuator(
		fanOut,
		[]routeoperator.StaticRouteConfig{{
			Prefix:      "192.0.2.0/24",
			NexthopAddr: "192.0.2.1",
			Interface:   "static0",
		}},
		5*time.Second,
		func() neigh.TableSnapshot { return dynamicRIBSnapshot().Neighbours },
		first,
		second,
	)

	require.NoError(t, applyGatewayActuator(t, actuator, emptyFIBSnapshot()))

	firstState := firstGateway.state()
	secondState := secondGateway.state()
	require.Equal(t, 1, firstState.staticRouteCalls)
	require.Equal(t, 1, secondState.staticRouteCalls)
	require.Len(t, firstState.fibRequests, 1)
	require.Len(t, secondState.fibRequests, 1)
	require.Equal(t, 1, firstState.functionUpdates)
	require.Equal(t, 1, secondState.functionUpdates)
}
