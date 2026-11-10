package operator_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/experimental"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/testutils/memnet"
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

// startFakeGateway serves a fake gateway on a loopback port until the test
// ends and returns it with its endpoint.
func startFakeGateway(t *testing.T) (*fakeGateway, string) {
	t.Helper()

	listener, err := net.Listen("tcp", "[::1]:0")
	require.NoError(t, err)

	return serveFakeGateway(t, listener), listener.Addr().String()
}

// startMemoryGateway serves a fake gateway called name on network until the
// test ends and returns it with the config that reaches it through network.
func startMemoryGateway(t *testing.T, network *memnet.Network, name string) (*fakeGateway, operator.GatewayConfig) {
	t.Helper()

	gateway := serveFakeGateway(t, network.Listen(name))
	return gateway, operator.GatewayConfig{
		Name:     name,
		Endpoint: xcfg.MustNonEmptyString("passthrough:///" + name),
	}
}

// serveFakeGateway serves a new fake gateway on listener until the test ends.
func serveFakeGateway(t *testing.T, listener net.Listener) *fakeGateway {
	t.Helper()

	gateway := &fakeGateway{functions: map[string]*ynpb.Function{}}
	server := grpc.NewServer()
	ynpb.RegisterGatewayServer(server, gateway)
	ynpb.RegisterFunctionServiceServer(server, &gatewayFunctionServer{gateway: gateway})
	routepb.RegisterRouteServiceServer(server, &gatewayRouteServer{gateway: gateway})
	var workers errgroup.Group
	workers.Go(func() error {
		// A test that never reaches the gateway may stop it before it
		// serves.
		if err := server.Serve(listener); !errors.Is(err, grpc.ErrServerStopped) {
			return err
		}
		return nil
	})
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, workers.Wait())
	})

	return gateway
}

// memoryOperator is the address the operator serves on in a memory network.
const memoryOperator = "operator"

// runMemoryOperator starts the operator cfg describes serving on network
// and reaching its gateways through it, stops it when the test ends, and
// returns a client connection to it.
//
// It points the server endpoint of cfg at the address it serves on.
func runMemoryOperator(t *testing.T, network *memnet.Network, cfg *op.Config) *grpc.ClientConn {
	t.Helper()

	cfg.Server.Endpoint = xcfg.MustNonEmptyString(memoryOperator)
	runnable, err := op.NewOperator(cfg,
		op.WithListener(network.Listen(memoryOperator)),
		op.WithGatewayDialOptions(grpc.WithContextDialer(network.Dial)),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	var workers errgroup.Group
	workers.Go(func() error {
		return runnable.Run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		_ = workers.Wait()
		require.NoError(t, runnable.Close())
	})

	connection, err := grpc.NewClient("passthrough:///"+memoryOperator,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(network.Dial),
		// The operator answers in the best coding a client accepts, and the
		// zstd codec pools its decoders process-wide: a decoder made in one
		// bubble must never run in another.
		grpc.WithDefaultCallOptions(experimental.AcceptCompressors(gzip.Name)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	return connection
}

// settle lets one rebuild window of fake time pass and waits until the
// bubble is idle, so a change made before it has been built and applied.
func settle() {
	time.Sleep(operator.RebuildWindow)
	synctest.Wait()
}

// requestContext gives each synchronous test RPC its own bounded lifetime.
func requestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// requireFIBClassifications checks build gauge values for one gateway/module,
// including the presence of zero-valued series.
func requireFIBClassifications(
	collector require.TestingT,
	metricList []*commonpb.Metric,
	gateway, module string,
	unresolved, excluded float64,
) {
	expected := map[string]float64{
		"route_operator_fib_unresolved_nexthops":    unresolved,
		"route_operator_fib_device_excluded_routes": excluded,
	}
	counts := map[string]int{}
	for _, metric := range metricList {
		value, ok := expected[metric.GetName()]
		if !ok {
			continue
		}
		labels := map[string]string{}
		for _, label := range metric.GetLabels() {
			labels[label.GetName()] = label.GetValue()
		}
		if labels["gateway"] != gateway || labels["module"] != module {
			continue
		}
		require.IsType(collector, &commonpb.Metric_Gauge{}, metric.GetValue())
		require.Equal(collector, value, metric.GetGauge(), "%s gateway=%s", metric.GetName(), gateway)
		counts[metric.GetName()]++
	}
	for name := range expected {
		require.Equal(collector, 1, counts[name],
			"expected exactly one %s series for gateway=%s module=%s", name, gateway, module,
		)
	}
}

// Test_Operator_Metrics_ServesReconcileAndApplyFamilies verifies that the
// route operator serves the framework's reconcile and gateway apply
// families under their route_operator names.
func Test_Operator_Metrics_ServesReconcileAndApplyFamilies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := memnet.NewNetwork()
		_, gateway := startMemoryGateway(t, network, "gw0")
		cfg := op.DefaultConfig()
		cfg.NetlinkMonitor.Disabled = true
		cfg.Readiness.ExpectBird = false
		cfg.Gateways = []operator.GatewayConfig{gateway}
		cfg.Register.Interval = xcfg.MustNonZero(time.Hour)
		connection := runMemoryOperator(t, network, cfg)

		response, err := operatorpb.NewMetricsServiceClient(connection).GetMetrics(
			requestContext(t), &commonpb.GetMetricsRequest{}, grpc.WaitForReady(true),
		)
		require.NoError(t, err)

		served := map[string]bool{}
		for _, metric := range response.GetMetrics() {
			served[metric.GetName()] = true
		}
		for _, name := range []string{
			"route_operator_reconcile_total",
			"route_operator_reconcile_errors_total",
			"route_operator_backoff_seconds",
			"route_operator_state",
			"route_operator_gateway_apply_total",
			"route_operator_gateway_apply_errors_total",
			"route_operator_gateway_apply_duration_seconds",
		} {
			require.True(t, served[name], "metric family %s is not served", name)
		}
	})
}

// Test_Operator_UnflushedRoute_ReachesGateway verifies that a route inserted
// without a flush still reaches the gateway by the next reconcile interval.
func Test_Operator_UnflushedRoute_ReachesGateway(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := memnet.NewNetwork()
		gateway, gatewayConfig := startMemoryGateway(t, network, "gw0")
		cfg := op.DefaultConfig()
		cfg.NetlinkMonitor.Disabled = true
		cfg.Readiness.ExpectBird = false
		cfg.Gateways = []operator.GatewayConfig{gatewayConfig}
		cfg.Reconcile.Interval = xcfg.MustNonZero(100 * time.Millisecond)
		cfg.Register.Interval = xcfg.MustNonZero(time.Hour)
		cfg.Static.Routes = []op.StaticRouteConfig{staticPath("10.10.0.0/24", "192.0.2.1")}
		connection := runMemoryOperator(t, network, cfg)

		_, err := operatorpb.NewNeighbourServiceClient(connection).SwapNeighbours(requestContext(t), &operatorpb.SwapNeighboursRequest{
			Table:   "static",
			Entries: entryRequest("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, "eth0").GetEntries(),
		}, grpc.WaitForReady(true))
		require.NoError(t, err)
		paths := []neigh.HardwareRoute{{
			SourceMAC:      [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			DestinationMAC: [6]byte{2, 0, 0, 0, 0, 1},
			Device:         "eth0",
		}}
		// The neighbour change is built and every build it still owes has
		// run before the route is inserted, so no reported change can carry
		// it.
		settle()
		requireFIB(t, gateway.lastFIBRequest(), "route0", map[string][]neigh.HardwareRoute{
			"10.10.0.0/24": paths,
		})
		time.Sleep(3 * operator.RebuildWindow)

		prefix, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix("10.20.0.0/24"))
		require.NoError(t, err)
		_, err = operatorpb.NewRouteServiceClient(connection).InsertRoute(requestContext(t), &operatorpb.InsertRouteRequest{
			Name:         "route0",
			Prefix:       prefix,
			NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.1"))},
			SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC,
		})
		require.NoError(t, err)

		time.Sleep(cfg.Reconcile.Interval.Unwrap())
		settle()
		requireFIB(t, gateway.lastFIBRequest(), "route0", map[string][]neigh.HardwareRoute{
			"10.10.0.0/24": paths,
			"10.20.0.0/24": paths,
		})
	})
}

// Test_Operator_StaticResolution_ReadinessLifecycle verifies that empty updates
// retain last-good forwarding until current desired routes resolve again.
//
// Every step is checked one rebuild window after its change.
func Test_Operator_StaticResolution_ReadinessLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := memnet.NewNetwork()
		gateway, gatewayConfig := startMemoryGateway(t, network, "gw0")

		prefix := netip.MustParsePrefix("10.10.0.0/24")
		nexthop := netip.MustParseAddr("10.0.0.1")

		module := "route-custom"

		// The long steady-state interval makes withdrawal after a
		// successful apply depend on change notifications rather than
		// periodic retries.
		cfg := op.DefaultConfig()
		cfg.Function.Module = xcfg.MustNonEmptyString(module)
		cfg.NetlinkMonitor.Disabled = true
		cfg.Readiness.ExpectBird = false
		cfg.Gateways = []operator.GatewayConfig{gatewayConfig}
		cfg.Reconcile.Interval = xcfg.MustNonZero(time.Hour)
		cfg.Register.Interval = xcfg.MustNonZero(time.Hour)
		cfg.Static.Routes = []op.StaticRouteConfig{{
			Prefix:      prefix.String(),
			NexthopAddr: nexthop.String(),
		}}

		conn := runMemoryOperator(t, network, cfg)
		readiness := operatorpb.NewReadinessServiceClient(conn)
		expected := map[string][]neigh.HardwareRoute{
			prefix.String(): {{
				SourceMAC:      [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
				DestinationMAC: [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}, Device: "eth0",
			}},
		}
		waitForState := func(state readinesspb.State, paths map[string][]neigh.HardwareRoute) {
			t.Helper()
			settle()
			response, err := readiness.Ready(requestContext(t), &readinesspb.ReadyRequest{
				Scopes: []string{"fib:gw0:" + module},
			}, grpc.WaitForReady(true))
			require.NoError(t, err)
			require.Len(t, response.GetScopes(), 1)
			scope := response.GetScopes()[0]
			require.Equal(t, "fib:gw0:"+module, scope.GetName())
			require.Equal(t, state, scope.GetState())
			if paths == nil {
				writes, functionUpdates := gateway.receivedWrites()
				require.Empty(t, writes)
				require.Zero(t, functionUpdates)
			} else {
				requireFIB(t, gateway.lastFIBRequest(), module, paths)
				require.Equal(t, []string{module}, gateway.functionModules(cfg.Function.Name.Unwrap()))
			}
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
		waitForState(readinesspb.State_STATE_DEGRADED, expected)
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
		waitForState(readinesspb.State_STATE_DEGRADED, expected)
		publication.Entries[0].LinkAddr = commonpb.NewMACAddressEUI48([6]byte{2, 0, 0, 0, 0, 42})
		_, err = neighbours.SwapNeighbours(requestContext(t), publication)
		require.NoError(t, err)

		expected[prefix.String()][0].DestinationMAC = [6]byte{2, 0, 0, 0, 0, 42}
		_, err = routes.InsertRoute(requestContext(t), &operatorpb.InsertRouteRequest{
			Name: module, Prefix: wirePrefix,
			NexthopAddrs: []*commonpb.IPAddress{commonpb.NewIPAddressFromAddr(nexthop)},
			SourceId:     operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC, DoFlush: true,
		})
		require.NoError(t, err)
		waitForState(readinesspb.State_STATE_READY, expected)
	})
}

// Test_Operator_StaticResolution_IndependentGateways verifies that local FIBs,
// readiness, and route classifications stay independent through loss and recovery.
func Test_Operator_StaticResolution_IndependentGateways(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := memnet.NewNetwork()
		firstGateway, firstConfig := startMemoryGateway(t, network, "gw0")
		secondGateway, secondConfig := startMemoryGateway(t, network, "gw1")
		config := op.DefaultConfig()
		config.NetlinkMonitor.Disabled = true
		config.Readiness.ExpectBird = false
		config.Gateways = []operator.GatewayConfig{firstConfig, secondConfig}
		config.GatewayDevices = map[string][]string{"gw0": {"eth0"}, "gw1": {"eth1"}}
		config.Static.Routes = []op.StaticRouteConfig{
			staticPath("10.10.0.0/24", "192.0.2.1"),
			staticPath("10.10.0.0/24", "192.0.2.2"),
		}
		config.Reconcile.Interval = xcfg.MustNonZero(time.Hour)
		connection := runMemoryOperator(t, network, config)
		neighbours := operatorpb.NewNeighbourServiceClient(connection)
		readiness := operatorpb.NewReadinessServiceClient(connection)
		metrics := operatorpb.NewMetricsServiceClient(connection)
		first := entryRequest("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, "eth0").GetEntries()
		second := entryRequest("192.0.2.2", [6]byte{2, 0, 0, 0, 0, 2}, "eth1").GetEntries()
		changedSecond := entryRequest("192.0.2.2", [6]byte{2, 0, 0, 0, 0, 22}, "eth1").GetEntries()
		latestSecond := entryRequest("192.0.2.2", [6]byte{2, 0, 0, 0, 0, 33}, "eth1").GetEntries()
		var firstRequest *routepb.UpdateFIBRequest
		for idx, tc := range []struct {
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
			t.Logf("step %d: %s", idx+1, tc.name)
			_, err := neighbours.SwapNeighbours(requestContext(t), &operatorpb.SwapNeighboursRequest{
				Table: "static", Entries: tc.entries,
			}, grpc.WaitForReady(true))
			require.NoError(t, err)
			settle()
			ctx := requestContext(t)
			response, err := readiness.Ready(ctx, &readinesspb.ReadyRequest{})
			require.NoError(t, err)
			states := map[string]readinesspb.State{}
			for _, scope := range response.GetScopes() {
				states[scope.GetName()] = scope.GetState()
			}
			require.Equal(t, tc.state, states["fib:gw0:route0"])
			require.Equal(t, readinesspb.State_STATE_READY, states["fib:gw1:route0"])
			firstPaths := map[string][]neigh.HardwareRoute{}
			if tc.firstReady || firstRequest != nil {
				firstPaths["10.10.0.0/24"] = []neigh.HardwareRoute{{
					SourceMAC:      [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
					DestinationMAC: [6]byte{2, 0, 0, 0, 0, 1}, Device: "eth0",
				}}
			}
			if len(firstPaths) == 0 {
				require.Nil(t, firstGateway.lastFIBRequest())
			} else {
				requireFIB(t, firstGateway.lastFIBRequest(), "route0", firstPaths)
			}
			requireFIB(t, secondGateway.lastFIBRequest(), "route0", map[string][]neigh.HardwareRoute{
				"10.10.0.0/24": {{
					SourceMAC:      [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
					DestinationMAC: tc.destination, Device: "eth1",
				}},
			})
			metricResponse, err := metrics.GetMetrics(ctx, &commonpb.GetMetricsRequest{})
			require.NoError(t, err)
			if tc.firstReady {
				requireFIBClassifications(t, metricResponse.GetMetrics(), "gw0", "route0", 0, 1)
				requireFIBClassifications(t, metricResponse.GetMetrics(), "gw1", "route0", 0, 1)
			} else {
				requireFIBClassifications(t, metricResponse.GetMetrics(), "gw0", "route0", 1, 1)
				requireFIBClassifications(t, metricResponse.GetMetrics(), "gw1", "route0", 1, 0)
			}
			if tc.firstReady {
				firstRequest = firstGateway.lastFIBRequest()
			}
		}

		secondRequest := secondGateway.lastFIBRequest()
		routes := operatorpb.NewRouteServiceClient(connection)
		prefix, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix("10.10.0.0/24"))
		require.NoError(t, err)
		_, err = routes.DeleteRoute(requestContext(t), &operatorpb.DeleteRouteRequest{
			Name: "route0", Prefix: prefix,
			NexthopAddrs: []*commonpb.IPAddress{
				commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.1")),
				commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.2")),
			},
			SourceId: operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC, DoFlush: true,
		})
		require.NoError(t, err)
		settle()
		ctx := requestContext(t)
		response, err := metrics.GetMetrics(ctx, &commonpb.GetMetricsRequest{})
		require.NoError(t, err)
		for _, gateway := range []string{"gw0", "gw1"} {
			requireFIBClassifications(t, response.GetMetrics(), gateway, "route0", 0, 0)
		}
		readinessResponse, err := readiness.Ready(ctx, &readinesspb.ReadyRequest{
			Scopes: []string{"fib:gw0:route0", "fib:gw1:route0"},
		})
		require.NoError(t, err)
		require.Len(t, readinessResponse.GetScopes(), 2)
		for _, scope := range readinessResponse.GetScopes() {
			require.Equal(t, readinesspb.State_STATE_DEGRADED, scope.GetState())
			require.Len(t, scope.GetReasons(), 1)
			require.Contains(t, scope.GetReasons()[0].GetMessage(), "no route records")
		}
		require.True(t, proto.Equal(firstRequest, firstGateway.lastFIBRequest()))
		require.True(t, proto.Equal(secondRequest, secondGateway.lastFIBRequest()))
	})
}

// Test_Operator_ExternalNeighbours_RefreshesAcceptedObservations verifies that
// complete publications refresh readiness independently of forwarding changes.
func Test_Operator_ExternalNeighbours_RefreshesAcceptedObservations(t *testing.T) {
	entries := entryRequest("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, "eth0").GetEntries()
	cases := []struct {
		name string
		// before are the publications accepted ahead of the one under test.
		before  [][]*operatorpb.NeighbourEntry
		entries []*operatorpb.NeighbourEntry
	}{
		{name: "first empty observation"},
		{name: "first populated observation", entries: entries},
		{name: "identical populated observation", before: [][]*operatorpb.NeighbourEntry{entries}, entries: entries},
		{name: "withdrawal observation", before: [][]*operatorpb.NeighbourEntry{entries}},
		{name: "identical empty observation", before: [][]*operatorpb.NeighbourEntry{nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				publisher := startNeighbourPublisher(t)
				for _, before := range tc.before {
					publisher.Publish(t, before)
				}
				previous := publisher.ReadScope(t)

				time.Sleep(time.Second)
				publisher.Publish(t, tc.entries)
				current := publisher.ReadScope(t)
				require.Equal(t, readinesspb.State_STATE_READY, current.GetState())
				require.True(t, current.GetObservedAt().AsTime().After(previous.GetObservedAt().AsTime()),
					"accepted publication must advance its observation timestamp")
				require.True(t, proto.Equal(previous.GetLastTransitionTime(), current.GetLastTransitionTime()),
					"a fresh observation must not create a state transition")
			})
		})
	}
}

// Test_Operator_ExternalNeighbours_RefusedPublicationKeepsObservation
// verifies that a refused or cancelled publication leaves the last
// successful observation intact.
func Test_Operator_ExternalNeighbours_RefusedPublicationKeepsObservation(t *testing.T) {
	invalid := entryRequest("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, "eth0").GetEntries()
	invalid[0].NextHop = &commonpb.IPAddress{Addr: []byte{1, 2, 3}}
	cases := []struct {
		name    string
		request *operatorpb.SwapNeighboursRequest
		// cancelled sends the request on a context cancelled beforehand.
		cancelled bool
		code      codes.Code
	}{
		{name: "unknown table", request: &operatorpb.SwapNeighboursRequest{Table: "missing"}, code: codes.NotFound},
		{name: "invalid snapshot", request: &operatorpb.SwapNeighboursRequest{Table: "publisher", Entries: invalid}, code: codes.InvalidArgument},
		{name: "cancelled publication", request: &operatorpb.SwapNeighboursRequest{Table: "publisher"}, cancelled: true, code: codes.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				publisher := startNeighbourPublisher(t)
				publisher.Publish(t, entryRequest("192.0.2.1", [6]byte{2, 0, 0, 0, 0, 1}, "eth0").GetEntries())
				previous := publisher.ReadScope(t)

				time.Sleep(time.Second)
				ctx, cancel := context.WithCancel(requestContext(t))
				if tc.cancelled {
					cancel()
				}
				defer cancel()
				_, err := publisher.Neighbours.SwapNeighbours(ctx, tc.request)
				require.Equal(t, tc.code, status.Code(err))
				require.True(t, proto.Equal(previous, publisher.ReadScope(t)),
					"a refused publication must not refresh readiness")
			})
		})
	}
}

// neighbourPublisher is an operator without a netlink monitor and the
// neighbour table publisher an external publisher owns on it.
type neighbourPublisher struct {
	Neighbours operatorpb.NeighbourServiceClient
	readiness  operatorpb.ReadinessServiceClient
}

// startNeighbourPublisher runs an operator in the bubble and creates the
// table publisher on it, requiring that creating the table, metadata only,
// observes nothing.
//
// Fake time stands still unless slept, so the table is created a second
// after the first observation.
func startNeighbourPublisher(t *testing.T) *neighbourPublisher {
	t.Helper()

	network := memnet.NewNetwork()
	_, gateway := startMemoryGateway(t, network, "gw0")
	config := op.DefaultConfig()
	config.NetlinkMonitor.Disabled = true
	config.Readiness.ExpectBird = false
	config.Gateways = []operator.GatewayConfig{gateway}
	config.Reconcile.Interval = xcfg.MustNonZero(time.Hour)
	connection := runMemoryOperator(t, network, config)
	publisher := &neighbourPublisher{
		Neighbours: operatorpb.NewNeighbourServiceClient(connection),
		readiness:  operatorpb.NewReadinessServiceClient(connection),
	}

	previous := publisher.ReadScope(t)
	require.Equal(t, readinesspb.State_STATE_READY, previous.GetState())
	time.Sleep(time.Second)
	_, err := publisher.Neighbours.CreateTable(requestContext(t), &operatorpb.CreateNeighbourTableRequest{Name: "publisher"})
	require.NoError(t, err)
	require.True(t, proto.Equal(previous, publisher.ReadScope(t)), "table metadata is not an observation")

	return publisher
}

// Publish replaces the whole publisher table with entries a second of fake
// time after the previous call.
func (m *neighbourPublisher) Publish(t *testing.T, entries []*operatorpb.NeighbourEntry) {
	t.Helper()

	time.Sleep(time.Second)
	_, err := m.Neighbours.SwapNeighbours(requestContext(t), &operatorpb.SwapNeighboursRequest{
		Table: "publisher", Entries: entries,
	})
	require.NoError(t, err)
}

// ReadScope returns the neighbours readiness scope.
func (m *neighbourPublisher) ReadScope(t *testing.T) *readinesspb.Scope {
	t.Helper()

	response, err := m.readiness.Ready(requestContext(t), &readinesspb.ReadyRequest{}, grpc.WaitForReady(true))
	require.NoError(t, err)
	for _, scope := range response.GetScopes() {
		if scope.GetName() == "neighbours" {
			return scope
		}
	}
	t.Fatal("neighbours readiness scope is missing")
	return nil
}
