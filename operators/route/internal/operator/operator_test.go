package operator_test

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	op "github.com/yanet-platform/yanet2/operators/route/internal/operator"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// fakeGateway accepts registrations, holds functions the way the real
// gateway does, and records every forwarding table pushed to it.
type fakeGateway struct {
	ynpb.UnimplementedGatewayServer

	mu              sync.Mutex
	fibs            []*routepb.UpdateFIBRequest
	functions       map[string]*ynpb.Function
	fibError        error
	functionError   error
	functionUpdates int
}

// failWrites makes subsequent forwarding and function writes fail independently.
func (m *fakeGateway) failWrites(fibError, functionError error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fibError, m.functionError = fibError, functionError
}

// receivedWrites snapshots attempted writes without sharing the request slice.
func (m *fakeGateway) receivedWrites() ([]*routepb.UpdateFIBRequest, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*routepb.UpdateFIBRequest(nil), m.fibs...), m.functionUpdates
}

// recordFIB records attempted forwarding writes even when programming fails.
func (m *fakeGateway) recordFIB(request *routepb.UpdateFIBRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fibs = append(m.fibs, proto.Clone(request).(*routepb.UpdateFIBRequest))
	return m.fibError
}

// storeFunction records attempts and retains only successfully published functions.
func (m *fakeGateway) storeFunction(function *ynpb.Function) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.functionUpdates++
	if m.functionError != nil {
		return m.functionError
	}
	m.functions[function.GetId().GetName()] = proto.Clone(function).(*ynpb.Function)
	return nil
}

// Register accepts every operator that asks.
func (m *fakeGateway) Register(
	ctx context.Context,
	req *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	return &ynpb.RegisterResponse{Status: ynpb.RegistrationStatus_REGISTRATION_STATUS_REGISTERED}, nil
}

// lastFIBRequest exposes the latest recorded request for read-only assertions.
func (m *fakeGateway) lastFIBRequest() *routepb.UpdateFIBRequest {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.fibs) == 0 {
		return nil
	}
	return m.fibs[len(m.fibs)-1]
}

// functionModules snapshots module names in a successfully stored function.
func (m *fakeGateway) functionModules(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var modules []string
	for _, chain := range m.functions[name].GetChains() {
		for _, module := range chain.GetChain().GetModules() {
			modules = append(modules, module.GetName())
		}
	}
	return modules
}

// gatewayRouteServer records the forwarding tables the gateway receives.
type gatewayRouteServer struct {
	routepb.UnimplementedRouteServiceServer

	gateway *fakeGateway
}

// UpdateFIB records the pushed table.
func (m *gatewayRouteServer) UpdateFIB(
	ctx context.Context,
	req *routepb.UpdateFIBRequest,
) (*routepb.UpdateFIBResponse, error) {
	return &routepb.UpdateFIBResponse{}, m.gateway.recordFIB(req)
}

// gatewayFunctionServer serves the functions the fake gateway holds.
type gatewayFunctionServer struct {
	ynpb.UnimplementedFunctionServiceServer

	gateway *fakeGateway
}

// Get returns a held function, or reports it missing.
func (m *gatewayFunctionServer) Get(
	ctx context.Context,
	req *ynpb.GetFunctionRequest,
) (*ynpb.GetFunctionResponse, error) {
	m.gateway.mu.Lock()
	defer m.gateway.mu.Unlock()

	function, ok := m.gateway.functions[req.GetId().GetName()]
	if !ok {
		return nil, status.Error(codes.NotFound, "no such function")
	}
	return &ynpb.GetFunctionResponse{Function: function}, nil
}

// Update stores the published function.
func (m *gatewayFunctionServer) Update(
	ctx context.Context,
	req *ynpb.UpdateFunctionRequest,
) (*ynpb.UpdateFunctionResponse, error) {
	return &ynpb.UpdateFunctionResponse{}, m.gateway.storeFunction(req.GetFunction())
}

// startFakeGateway serves the fake on a loopback port until the test ends
// and returns it with its endpoint.
func startFakeGateway(t *testing.T) (*fakeGateway, string) {
	t.Helper()

	gateway := &fakeGateway{functions: map[string]*ynpb.Function{}}
	listener, err := net.Listen("tcp", "[::1]:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	ynpb.RegisterGatewayServer(server, gateway)
	ynpb.RegisterFunctionServiceServer(server, &gatewayFunctionServer{gateway: gateway})
	routepb.RegisterRouteServiceServer(server, &gatewayRouteServer{gateway: gateway})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return gateway, listener.Addr().String()
}

// reserveEndpoint reserves a loopback port and hands it back released, so
// the operator binds it and the test can dial the same address.
func reserveEndpoint(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "[::1]:0")
	require.NoError(t, err)
	endpoint := listener.Addr().String()
	require.NoError(t, listener.Close())

	return endpoint
}

// runOperator starts the operator and stops it when the test ends.
func runOperator(t *testing.T, cfg *op.Config) {
	t.Helper()

	runnable, err := op.NewOperator(cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	var wg errgroup.Group
	wg.Go(func() error {
		return runnable.Run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		_ = wg.Wait()
		require.NoError(t, runnable.Close())
	})
}

// requestContext gives each synchronous test RPC its own bounded lifetime.
func requestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// Test_Operator_StaticResolution_ReadinessLifecycle verifies that neighbour
// arrival, withdrawal, and route deletion drive readiness with FIB publication.
func Test_Operator_StaticResolution_ReadinessLifecycle(t *testing.T) {
	gateway, gatewayEndpoint := startFakeGateway(t)
	operatorEndpoint := reserveEndpoint(t)

	prefix := netip.MustParsePrefix("10.10.0.0/24")
	nexthop := netip.MustParseAddr("10.0.0.1")

	module := "route-custom"

	// The long steady-state interval makes withdrawal after a successful
	// apply depend on change notifications rather than periodic retries.
	cfg := op.DefaultConfig()
	cfg.Function.Module = xcfg.MustNonEmptyString(module)
	cfg.NetlinkMonitor.Disabled = true
	cfg.Readiness.ExpectBird = false
	cfg.Server.Endpoint = xcfg.MustNonEmptyString(operatorEndpoint)
	cfg.Gateways = []operator.GatewayConfig{{
		Name:     "gw0",
		Endpoint: xcfg.MustNonEmptyString(gatewayEndpoint),
	}}
	cfg.Reconcile.Interval = xcfg.MustNonZero(time.Hour)
	cfg.Register.Interval = xcfg.MustNonZero(time.Hour)
	cfg.Static.Routes = []op.StaticRouteConfig{{
		Prefix:      prefix.String(),
		NexthopAddr: nexthop.String(),
	}}

	runOperator(t, cfg)

	conn, err := grpc.NewClient(operatorEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() {
		require.NoError(t, conn.Close())
	}()
	readiness := operatorpb.NewReadinessServiceClient(conn)
	expected := map[string][]neigh.HardwareRoute{
		prefix.String(): {{
			SourceMAC:      [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			DestinationMAC: [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}, Device: "eth0",
		}},
	}
	waitForState := func(state readinesspb.State, paths map[string][]neigh.HardwareRoute) {
		t.Helper()
		require.EventuallyWithT(t, func(collector *assert.CollectT) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			response, err := readiness.Ready(ctx, &readinesspb.ReadyRequest{
				Scopes: []string{"fib:gw0:" + module},
			}, grpc.WaitForReady(true))
			require.NoError(collector, err)
			require.Len(collector, response.GetScopes(), 1)
			scope := response.GetScopes()[0]
			require.Equal(collector, "fib:gw0:"+module, scope.GetName())
			require.Equal(collector, state, scope.GetState())
			requireFIB(collector, gateway.lastFIBRequest(), module, paths)
			require.Equal(collector, []string{module}, gateway.functionModules(cfg.Function.Name.Unwrap()))
		}, 10*time.Second, 20*time.Millisecond)
	}
	waitForState(readinesspb.State_STATE_NOT_READY, nil)
	response, err := readiness.Ready(requestContext(t), &readinesspb.ReadyRequest{Scopes: []string{"fib:gw0:" + module}})
	require.NoError(t, err)
	require.Len(t, response.GetScopes(), 1)
	require.Len(t, response.GetScopes()[0].GetReasons(), 1)
	reason := response.GetScopes()[0].GetReasons()[0]
	require.Equal(t, "APPLY_FAILED", reason.GetCode())
	require.Contains(t, reason.GetMessage(), "gw0")
	require.Contains(t, reason.GetMessage(), module)
	require.Contains(t, reason.GetMessage(), prefix.String())

	neighbours := operatorpb.NewNeighbourServiceClient(conn)
	publication := &operatorpb.SwapNeighboursRequest{
		Table: "static",
		Entries: []*operatorpb.NeighbourEntry{{
			NextHop:      commonpb.NewIPAddressFromAddr(nexthop),
			LinkAddr:     commonpb.NewMACAddressEUI48([6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}),
			HardwareAddr: commonpb.NewMACAddressEUI48([6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}),
			Device:       "eth0",
		}},
	}
	_, err = neighbours.UpdateNeighbours(requestContext(t), &operatorpb.UpdateNeighboursRequest{
		Table: "static", Entries: publication.GetEntries(),
	})
	require.NoError(t, err)
	waitForState(readinesspb.State_STATE_READY, expected)

	_, err = neighbours.SwapNeighbours(requestContext(t), &operatorpb.SwapNeighboursRequest{Table: "static"})
	require.NoError(t, err)
	waitForState(readinesspb.State_STATE_DEGRADED, nil)
	_, err = neighbours.SwapNeighbours(requestContext(t), publication)
	require.NoError(t, err)
	waitForState(readinesspb.State_STATE_READY, expected)

	wirePrefix, err := commonpb.NewIPPrefixFromPrefix(prefix)
	require.NoError(t, err)
	routes := operatorpb.NewRouteServiceClient(conn)
	_, err = routes.DeleteRoute(requestContext(t), &operatorpb.DeleteRouteRequest{
		Name: module, Prefix: wirePrefix,
		NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(nexthop)},
		SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC, DoFlush: true,
	})
	require.NoError(t, err)
	waitForState(readinesspb.State_STATE_DEGRADED, nil)
	_, err = routes.InsertRoute(requestContext(t), &operatorpb.InsertRouteRequest{
		Name: module, Prefix: wirePrefix,
		NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(nexthop)},
		SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC, DoFlush: true,
	})
	require.NoError(t, err)
	waitForState(readinesspb.State_STATE_READY, expected)
}

// Test_Operator_StaticResolution_IndependentGateways verifies that unresolved
// gateways cannot suppress healthy gateways' readiness or hardware-route updates.
func Test_Operator_StaticResolution_IndependentGateways(t *testing.T) {
	firstGateway, firstEndpoint := startFakeGateway(t)
	secondGateway, secondEndpoint := startFakeGateway(t)
	config := op.DefaultConfig()
	config.NetlinkMonitor.Disabled = true
	config.Readiness.ExpectBird = false
	endpoint := reserveEndpoint(t)
	config.Server.Endpoint = xcfg.MustNonEmptyString(endpoint)
	config.Gateways = []operator.GatewayConfig{
		{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(firstEndpoint)},
		{Name: "gw1", Endpoint: xcfg.MustNonEmptyString(secondEndpoint)},
	}
	config.GatewayDevices = map[string][]string{"gw0": {"eth0"}, "gw1": {"eth1"}}
	config.Static.Routes = []op.StaticRouteConfig{
		staticPath("10.10.0.0/24", "192.0.2.1"),
		staticPath("10.10.0.0/24", "192.0.2.2"),
	}
	config.Reconcile.Interval = xcfg.MustNonZero(time.Hour)
	runOperator(t, config)
	connection, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	neighbours := operatorpb.NewNeighbourServiceClient(connection)
	readiness := operatorpb.NewReadinessServiceClient(connection)
	first := entryRequest("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, "eth0").GetEntries()
	second := entryRequest("192.0.2.2", [6]byte{2, 0, 0, 0, 0, 2}, "eth1").GetEntries()
	changedSecond := entryRequest("192.0.2.2", [6]byte{2, 0, 0, 0, 0, 22}, "eth1").GetEntries()
	latestSecond := entryRequest("192.0.2.2", [6]byte{2, 0, 0, 0, 0, 33}, "eth1").GetEntries()
	for _, tc := range []struct {
		name        string
		entries     []*operatorpb.NeighbourEntry
		state       readinesspb.State
		firstReady  bool
		destination [6]byte
	}{
		{
			name: "only second gateway resolves", entries: second,
			state: readinesspb.State_STATE_NOT_READY, destination: [6]byte{2, 0, 0, 0, 0, 2},
		},
		{
			name: "healthy gateway updates its destination while first remains unresolved", entries: changedSecond,
			state: readinesspb.State_STATE_NOT_READY, destination: [6]byte{2, 0, 0, 0, 0, 22},
		},
		{
			name: "first gateway recovers", entries: append(first, changedSecond...),
			state: readinesspb.State_STATE_READY, firstReady: true, destination: [6]byte{2, 0, 0, 0, 0, 22},
		},
		{
			name: "first gateway loses its last local path", entries: changedSecond,
			state: readinesspb.State_STATE_DEGRADED, destination: [6]byte{2, 0, 0, 0, 0, 22},
		},
		{
			name: "healthy gateway updates its destination while first remains degraded", entries: latestSecond,
			state: readinesspb.State_STATE_DEGRADED, destination: [6]byte{2, 0, 0, 0, 0, 33},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := neighbours.SwapNeighbours(requestContext(t), &operatorpb.SwapNeighboursRequest{
				Table: "static", Entries: tc.entries,
			}, grpc.WaitForReady(true))
			require.NoError(t, err)
			require.EventuallyWithT(t, func(collector *assert.CollectT) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				response, err := readiness.Ready(ctx, &readinesspb.ReadyRequest{})
				require.NoError(collector, err)
				states := map[string]readinesspb.State{}
				for _, scope := range response.GetScopes() {
					states[scope.GetName()] = scope.GetState()
				}
				require.Equal(collector, tc.state, states["fib:gw0:route0"])
				require.Equal(collector, readinesspb.State_STATE_READY, states["fib:gw1:route0"])
				firstPaths := map[string][]neigh.HardwareRoute{}
				if tc.firstReady {
					firstPaths["10.10.0.0/24"] = []neigh.HardwareRoute{{
						SourceMAC:      [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						DestinationMAC: [6]byte{2, 0, 0, 0, 0, 1}, Device: "eth0",
					}}
				}
				requireFIB(collector, firstGateway.lastFIBRequest(), "route0", firstPaths)
				requireFIB(collector, secondGateway.lastFIBRequest(), "route0", map[string][]neigh.HardwareRoute{
					"10.10.0.0/24": {{
						SourceMAC:      [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
						DestinationMAC: tc.destination, Device: "eth1",
					}},
				})
			}, 10*time.Second, 20*time.Millisecond)
		})
	}
}

// Test_Operator_ExternalNeighbours_RefreshesAcceptedObservations verifies that
// complete publications refresh readiness independently of forwarding changes,
// while refused publications leave the last successful observation intact.
func Test_Operator_ExternalNeighbours_RefreshesAcceptedObservations(t *testing.T) {
	_, gatewayEndpoint := startFakeGateway(t)
	operatorEndpoint := reserveEndpoint(t)
	config := op.DefaultConfig()
	config.NetlinkMonitor.Disabled = true
	config.Readiness.ExpectBird = false
	config.Server.Endpoint = xcfg.MustNonEmptyString(operatorEndpoint)
	config.Gateways = []operator.GatewayConfig{{
		Name: "gw0", Endpoint: xcfg.MustNonEmptyString(gatewayEndpoint),
	}}
	config.Reconcile.Interval = xcfg.MustNonZero(time.Hour)
	runOperator(t, config)

	connection, err := grpc.NewClient(operatorEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	neighbours := operatorpb.NewNeighbourServiceClient(connection)
	readiness := operatorpb.NewReadinessServiceClient(connection)
	readScope := func() *readinesspb.Scope {
		response, err := readiness.Ready(ctx, &readinesspb.ReadyRequest{}, grpc.WaitForReady(true))
		require.NoError(t, err)
		for _, scope := range response.GetScopes() {
			if scope.GetName() == "neighbours" {
				return scope
			}
		}
		t.Fatal("neighbours readiness scope is missing")
		return nil
	}
	previous := readScope()
	require.Equal(t, readinesspb.State_STATE_READY, previous.GetState())
	_, err = neighbours.CreateTable(ctx, &operatorpb.CreateNeighbourTableRequest{Name: "publisher"})
	require.NoError(t, err)
	require.True(t, proto.Equal(previous, readScope()), "table metadata is not an observation")

	entries := entryRequest("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, "eth0").GetEntries()
	for _, tc := range []struct {
		name    string
		entries []*operatorpb.NeighbourEntry
	}{
		{name: "first empty observation"},
		{name: "first populated observation", entries: entries},
		{name: "identical populated observation", entries: entries},
		{name: "withdrawal observation"},
		{name: "identical empty observation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := neighbours.SwapNeighbours(ctx, &operatorpb.SwapNeighboursRequest{
				Table: "publisher", Entries: tc.entries,
			})
			require.NoError(t, err)
			current := readScope()
			require.Equal(t, readinesspb.State_STATE_READY, current.GetState())
			require.True(t, current.GetObservedAt().AsTime().After(previous.GetObservedAt().AsTime()),
				"accepted publication must advance its observation timestamp")
			require.True(t, proto.Equal(previous.GetLastTransitionTime(), current.GetLastTransitionTime()),
				"a fresh observation must not create a state transition")
			previous = current
		})
	}

	invalid := entryRequest("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, "eth0").GetEntries()
	invalid[0].NextHop = &commonpb.IPAddress{Addr: []byte{1, 2, 3}}
	for _, tc := range []struct {
		name    string
		request *operatorpb.SwapNeighboursRequest
		code    codes.Code
	}{
		{name: "unknown table", request: &operatorpb.SwapNeighboursRequest{Table: "missing"}, code: codes.NotFound},
		{name: "invalid snapshot", request: &operatorpb.SwapNeighboursRequest{Table: "publisher", Entries: invalid}, code: codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := neighbours.SwapNeighbours(ctx, tc.request)
			require.Equal(t, tc.code, status.Code(err))
			require.True(t, proto.Equal(previous, readScope()), "a rejected publication must not refresh readiness")
		})
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	_, err = neighbours.SwapNeighbours(cancelled, &operatorpb.SwapNeighboursRequest{Table: "publisher"})
	require.Equal(t, codes.Canceled, status.Code(err))
	require.True(t, proto.Equal(previous, readScope()), "a cancelled publication must not refresh readiness")
}
