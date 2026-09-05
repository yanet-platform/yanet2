package operator_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	routeoperator "github.com/yanet-platform/yanet2/operators/route/internal/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// runRecordingOperator starts the real route operator with local discovery
// disabled and connects to the endpoint it registers with the recording gateway.
func runRecordingOperator(
	t *testing.T,
	config *routeoperator.Config,
	gateway *recordingGateway,
) *grpc.ClientConn {
	t.Helper()
	config.NetlinkMonitor.Disabled = true
	config.Readiness.ExpectBird = false
	config.Readiness.SampleInterval = 10 * time.Millisecond
	config.Server.Endpoint = xcfg.MustNonEmptyString("127.0.0.1:0")
	config.Reconcile.Interval = xcfg.MustNonZero(20 * time.Millisecond)
	config.Reconcile.InitialBackoff = xcfg.MustNonZero(10 * time.Millisecond)
	config.Reconcile.MaxBackoff = xcfg.MustNonZero(20 * time.Millisecond)
	config.NetlinkSidecar.UpdateTimeout = time.Second
	require.NoError(t, config.Validate())

	operator, err := routeoperator.NewOperator(config)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error {
		err := operator.Run(ctx)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	})
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
		require.NoError(t, operator.Close())
	})

	require.Eventually(t, func() bool {
		return gateway.state().registeredEndpoint != ""
	}, 3*time.Second, 5*time.Millisecond)
	connection, err := commonoperator.DialGateway(commonoperator.GatewayConfig{
		Name:     "route-operator",
		Endpoint: xcfg.MustNonEmptyString(gateway.state().registeredEndpoint),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return connection
}

// requireGatewayFIB waits for an exact serialized FIB on a recording gateway.
func requireGatewayFIB(t *testing.T, gateway *recordingGateway, expected []*routepb.FIBEntry) {
	t.Helper()
	require.EventuallyWithT(t, func(collector *assert.CollectT) {
		requests := gateway.state().fibRequests
		require.NotEmpty(collector, requests)
		actual := requests[len(requests)-1]
		require.True(collector, proto.Equal(&routepb.UpdateFIBRequest{
			ModuleName: "route0",
			Entries:    expected,
		}, actual), "unexpected FIB: %v", actual)
	}, 3*time.Second, 5*time.Millisecond)
}

// requireReadiness waits for the requested scope to report an expected state.
func requireReadiness(
	t *testing.T,
	client operatorpb.ReadinessServiceClient,
	name string,
	expected readinesspb.State,
) {
	t.Helper()
	require.EventuallyWithT(t, func(collector *assert.CollectT) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		response, err := client.Ready(ctx, &readinesspb.ReadyRequest{Scopes: []string{name}})
		require.NoError(collector, err)
		require.Len(collector, response.GetScopes(), 1)
		require.Equal(collector, expected, response.GetScopes()[0].GetState())
	}, 3*time.Second, 5*time.Millisecond)
}

// Test_Operator_StaticEgress_TwoNUMACollision verifies that identical link-local
// next hops on different NUMA devices cannot exchange interface-bound prefixes.
func Test_Operator_StaticEgress_TwoNUMACollision(t *testing.T) {
	firstGateway := &recordingGateway{}
	secondGateway := &recordingGateway{}
	config := routeoperator.DefaultConfig()
	config.NetlinkSidecar.Enabled = true
	config.Gateways = []commonoperator.GatewayConfig{
		{Name: "numa0", Endpoint: xcfg.MustNonEmptyString(serveRecordingGateway(t, firstGateway))},
		{Name: "numa1", Endpoint: xcfg.MustNonEmptyString(serveRecordingGateway(t, secondGateway))},
	}
	config.GatewayDevices = map[string][]string{"numa0": {"device0"}, "numa1": {"device1"}}
	config.LinkMap = map[string]string{"kni0": "device0", "kni1": "device1"}
	config.Static.Routes = []routeoperator.StaticRouteConfig{
		{Prefix: "2001:db8:a::/64", NexthopAddr: "fe80::1", Interface: "kni0"},
		{Prefix: "2001:db8:b::/64", NexthopAddr: "fe80::1", Interface: "kni1"},
	}
	connection := runRecordingOperator(t, config, firstGateway)
	neighbours := operatorpb.NewNeighbourServiceClient(connection)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	for idx, device := range []string{"device0", "device1"} {
		table := "netlink-dataplane-" + device
		_, err := neighbours.CreateTable(ctx, &operatorpb.CreateNeighbourTableRequest{
			Name: table, DefaultPriority: 100,
		})
		require.NoError(t, err)
		_, err = neighbours.UpdateNeighbours(ctx, &operatorpb.UpdateNeighboursRequest{
			Table: table,
			Entries: []*operatorpb.NeighbourEntry{{
				NextHop:      commonpb.NewIPAddressFromAddr(netip.MustParseAddr("fe80::1")),
				HardwareAddr: commonpb.NewMACAddressEUI48([6]byte{2, 0, 0, 0, 0, byte(idx + 1)}),
				LinkAddr:     commonpb.NewMACAddressEUI48([6]byte{2, 0, 0, 0, 1, byte(idx + 1)}),
				Device:       device,
				State:        operatorpb.NeighbourState_NUD_REACHABLE,
			}},
		})
		require.NoError(t, err)
		response, err := neighbours.List(ctx, &operatorpb.ListNeighboursRequest{Table: table})
		require.NoError(t, err)
		require.Len(t, response.GetNeighbours(), 1)
		require.Equal(t, table, response.GetNeighbours()[0].GetSource())
		require.Equal(t, uint32(100), response.GetNeighbours()[0].GetPriority())
		require.Equal(t, operatorpb.NeighbourState_NUD_PERMANENT, response.GetNeighbours()[0].GetState())
	}

	for idx, gateway := range []*recordingGateway{firstGateway, secondGateway} {
		prefix := []string{"2001:db8:a::", "2001:db8:b::"}[idx]
		last := []string{"2001:db8:a:0:ffff:ffff:ffff:ffff", "2001:db8:b:0:ffff:ffff:ffff:ffff"}[idx]
		requireGatewayFIB(t, gateway, []*routepb.FIBEntry{{
			Range: commonpb.MustIPRange(netip.MustParseAddr(prefix), netip.MustParseAddr(last)),
			Nexthops: []*routepb.FIBNexthop{{
				SrcMac: commonpb.NewMACAddressEUI48([6]byte{2, 0, 0, 0, 0, byte(idx + 1)}),
				DstMac: commonpb.NewMACAddressEUI48([6]byte{2, 0, 0, 0, 1, byte(idx + 1)}),
				Device: []string{"device0", "device1"}[idx],
			}},
		}})
	}
}

// Test_Operator_Readiness_PrerequisiteFailureRecovery verifies that whole-pass
// failures are visible without replacing independent gateway apply outcomes.
func Test_Operator_Readiness_PrerequisiteFailureRecovery(t *testing.T) {
	failure := status.Error(codes.Unavailable, "sidecar unavailable")
	firstGateway := &recordingGateway{staticRouteError: failure}
	secondGateway := &recordingGateway{staticRouteError: failure}
	config := routeoperator.DefaultConfig()
	config.NetlinkSidecar.Enabled = true
	config.Gateways = []commonoperator.GatewayConfig{
		{Name: "numa0", Endpoint: xcfg.MustNonEmptyString(serveRecordingGateway(t, firstGateway))},
		{Name: "numa1", Endpoint: xcfg.MustNonEmptyString(serveRecordingGateway(t, secondGateway))},
	}
	config.Static.Routes = []routeoperator.StaticRouteConfig{{
		Prefix: "192.0.2.0/24", NexthopAddr: "192.0.2.1", Interface: "kni0",
	}}
	connection := runRecordingOperator(t, config, firstGateway)
	readiness := operatorpb.NewReadinessServiceClient(connection)
	requireReadiness(t, readiness, "reconcile", readinesspb.State_STATE_NOT_READY)
	requireReadiness(t, readiness, "fib:numa0:route0", readinesspb.State_STATE_UNKNOWN)
	require.Empty(t, firstGateway.state().fibRequests)
	require.Empty(t, secondGateway.state().fibRequests)
	watchCtx, cancelWatch := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelWatch()
	watch, err := readiness.Watch(watchCtx, &readinesspb.ReadyRequest{Scopes: []string{"reconcile"}})
	require.NoError(t, err)
	initial, err := watch.Recv()
	require.NoError(t, err)
	require.Len(t, initial.GetScopes(), 1)
	require.Equal(t, readinesspb.State_STATE_NOT_READY, initial.GetScopes()[0].GetState())
	require.Len(t, initial.GetScopes()[0].GetReasons(), 1)
	require.Equal(t, "APPLY_FAILED", initial.GetScopes()[0].GetReasons()[0].GetCode())

	firstGateway.setErrors(nil, nil)
	secondGateway.setErrors(nil, nil)
	requireReadiness(t, readiness, "reconcile", readinesspb.State_STATE_READY)
	requireReadiness(t, readiness, "fib:numa0:route0", readinesspb.State_STATE_READY)
	requireReadiness(t, readiness, "fib:numa1:route0", readinesspb.State_STATE_READY)
	recovered, err := watch.Recv()
	require.NoError(t, err)
	require.Len(t, recovered.GetScopes(), 1)
	require.Equal(t, readinesspb.State_STATE_READY, recovered.GetScopes()[0].GetState())

	firstGateway.setErrors(failure, nil)
	secondGateway.setErrors(failure, nil)
	requireReadiness(t, readiness, "reconcile", readinesspb.State_STATE_DEGRADED)
	requireReadiness(t, readiness, "fib:numa0:route0", readinesspb.State_STATE_READY)
	requireReadiness(t, readiness, "fib:numa1:route0", readinesspb.State_STATE_READY)
	degraded, err := watch.Recv()
	require.NoError(t, err)
	require.Len(t, degraded.GetScopes(), 1)
	require.Equal(t, readinesspb.State_STATE_DEGRADED, degraded.GetScopes()[0].GetState())

	firstGateway.setErrors(nil, nil)
	secondGateway.setErrors(nil, failure)
	requireReadiness(t, readiness, "fib:numa1:route0", readinesspb.State_STATE_DEGRADED)
	requireReadiness(t, readiness, "fib:numa0:route0", readinesspb.State_STATE_READY)
	requireReadiness(t, readiness, "reconcile", readinesspb.State_STATE_DEGRADED)

	secondGateway.setErrors(nil, nil)
	requireReadiness(t, readiness, "reconcile", readinesspb.State_STATE_READY)
	requireReadiness(t, readiness, "fib:numa1:route0", readinesspb.State_STATE_READY)
}
