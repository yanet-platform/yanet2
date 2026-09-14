package operator_test

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

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
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	op "github.com/yanet-platform/yanet2/operators/route/internal/operator"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// fakeGateway accepts registrations, holds functions the way the real
// gateway does, and records every forwarding table pushed to it.
type fakeGateway struct {
	ynpb.UnimplementedGatewayServer

	mu        sync.Mutex
	fibs      []*routepb.UpdateFIBRequest
	functions map[string]*ynpb.Function
}

// Register accepts every operator that asks.
func (m *fakeGateway) Register(
	ctx context.Context,
	req *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	return &ynpb.RegisterResponse{Status: ynpb.RegistrationStatus_REGISTRATION_STATUS_REGISTERED}, nil
}

// lastFIB returns the entries of the most recent push, and whether any
// push has arrived.
func (m *fakeGateway) lastFIB() ([]*routepb.FIBEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.fibs) == 0 {
		return nil, false
	}
	return m.fibs[len(m.fibs)-1].GetEntries(), true
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
	m.gateway.mu.Lock()
	defer m.gateway.mu.Unlock()

	m.gateway.fibs = append(m.gateway.fibs, proto.Clone(req).(*routepb.UpdateFIBRequest))
	return &routepb.UpdateFIBResponse{}, nil
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
	m.gateway.mu.Lock()
	defer m.gateway.mu.Unlock()

	function := proto.Clone(req.GetFunction()).(*ynpb.Function)
	m.gateway.functions[function.GetId().GetName()] = function
	return &ynpb.UpdateFunctionResponse{}, nil
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

// Test_Operator_NeighbourOverGRPC_PushesTheFIBBeforeTheInterval verifies
// that configuring the neighbour a seeded route needs publishes the route
// long before the reconcile interval would come round.
func Test_Operator_NeighbourOverGRPC_PushesTheFIBBeforeTheInterval(t *testing.T) {
	gateway, gatewayEndpoint := startFakeGateway(t)
	operatorEndpoint := reserveEndpoint(t)

	prefix := netip.MustParsePrefix("10.10.0.0/24")
	nexthop := netip.MustParseAddr("10.0.0.1")

	// The interval is far longer than the test, so a table reaching the
	// gateway proves the neighbour woke the reconcile loop.
	cfg := op.DefaultConfig()
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

	// The route is seeded but its nexthop resolves to nothing, so the first
	// pass publishes an empty table.
	require.Eventually(t, func() bool {
		entries, ok := gateway.lastFIB()
		return ok && len(entries) == 0
	}, 10*time.Second, 20*time.Millisecond, "the operator did not publish its first table")

	conn, err := grpc.NewClient(operatorEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() {
		require.NoError(t, conn.Close())
	}()

	_, err = operatorpb.NewNeighbourServiceClient(conn).UpdateNeighbours(
		t.Context(),
		&operatorpb.UpdateNeighboursRequest{
			Entries: []*operatorpb.NeighbourEntry{{
				NextHop:      commonpb.NewIPAddressFromAddr(nexthop),
				LinkAddr:     commonpb.NewMACAddressEUI48([6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}),
				HardwareAddr: commonpb.NewMACAddressEUI48([6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}),
				Device:       "eth0",
			}},
		},
	)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		entries, ok := gateway.lastFIB()
		return ok && len(entries) == 1
	}, 10*time.Second, 20*time.Millisecond, "the neighbour did not reach the gateway before the interval")

	entries, _ := gateway.lastFIB()
	start, end, err := entries[0].GetRange().ToRange()
	require.NoError(t, err)
	require.Equal(t, prefix.Addr(), start)
	require.Equal(t, netip.MustParseAddr("10.10.0.255"), end)
	require.Equal(t, "eth0", entries[0].GetNexthops()[0].GetDevice())
}
