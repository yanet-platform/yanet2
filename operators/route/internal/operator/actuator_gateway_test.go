package operator_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// gatewayFIBSink records successful writes and can hold a failed RPC in flight.
type gatewayFIBSink struct {
	routepb.UnimplementedRouteServiceServer
	FIBs         chan *routepb.UpdateFIBRequest
	BeforeUpdate func(context.Context, *routepb.UpdateFIBRequest) error
}

func (m *gatewayFIBSink) UpdateFIB(ctx context.Context, request *routepb.UpdateFIBRequest) (*routepb.UpdateFIBResponse, error) {
	if m.BeforeUpdate != nil {
		if err := m.BeforeUpdate(ctx, request); err != nil {
			return nil, err
		}
	}
	m.FIBs <- request
	return &routepb.UpdateFIBResponse{}, nil
}

// gatewayFunctionSink counts function writes independently of FIB writes.
type gatewayFunctionSink struct {
	ynpb.UnimplementedFunctionServiceServer
	Updates atomic.Int64
}

func (m *gatewayFunctionSink) Get(ctx context.Context, request *ynpb.GetFunctionRequest) (*ynpb.GetFunctionResponse, error) {
	return nil, status.Error(codes.NotFound, "function is not installed")
}

func (m *gatewayFunctionSink) Update(ctx context.Context, request *ynpb.UpdateFunctionRequest) (*ynpb.UpdateFunctionResponse, error) {
	m.Updates.Add(1)
	return &ynpb.UpdateFunctionResponse{}, nil
}

// newGatewayActuatorFixture connects the production actuator to isolated sinks.
func newGatewayActuatorFixture(t *testing.T, sink *gatewayFIBSink, options ...operator.GatewayActuatorOption) (*operator.GatewayActuator, *gatewayFunctionSink) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	functions := &gatewayFunctionSink{}
	routepb.RegisterRouteServiceServer(server, sink)
	ynpb.RegisterFunctionServiceServer(server, functions)
	var group errgroup.Group
	group.Go(func() error {
		err := server.Serve(listener)
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	})
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, group.Wait())
	})
	options = append(options, operator.WithGatewayActuatorFunction(operator.FunctionConfig{
		Name: xcfg.MustNonEmptyString("fn:route"), Chain: xcfg.MustNonEmptyString("default"),
		Module: xcfg.MustNonEmptyString("route0"), Weight: 1,
	}))
	actuator, err := operator.NewGatewayActuator(commonoperator.GatewayConfig{
		Name: "gateway", Endpoint: xcfg.MustNonEmptyString(listener.Addr().String()),
	}, options...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, actuator.Close()) })
	return actuator, functions
}

// gatewayRIBSnapshot supplies one module whose FIB depends on the remote input.
type gatewayRIBSnapshot struct{ RIB *rib.RIB }

func (m gatewayRIBSnapshot) Snapshot() map[string]*rib.RIB {
	return map[string]*rib.RIB{"route0": m.RIB}
}

// observedGatewayActuator exposes completion of real reconcile attempts.
type observedGatewayActuator struct {
	*operator.GatewayActuator
	Results chan error
}

func (m *observedGatewayActuator) Apply(ctx context.Context, snapshot operator.RouteSnapshot) error {
	err := m.GatewayActuator.Apply(ctx, snapshot)
	m.Results <- err
	return err
}

// awaitGatewayResult bounds waits for asynchronous RPC and reconcile progress.
func awaitGatewayResult[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case result := <-channel:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("gateway operation did not finish")
		var zero T
		return zero
	}
}

// Test_GatewayActuator_StaleRetry verifies that a new retry after input expiry
// preserves the installed FIB until a fresh complete snapshot arrives.
func Test_GatewayActuator_StaleRetry(t *testing.T) {
	tracker := readiness.NewTracker([]readiness.ScopeSpec{{Name: "neighbours"}})
	input := operator.NewNeighbourReadiness("remote", 250*time.Millisecond, tracker)
	fixture := newNeighbourServiceFixture(t,
		operator.WithNeighbourServiceRemoteSource("remote", []string{"logical0"}),
		operator.WithNeighbourServiceOnSnapshotReceived(input.OnSnapshotReceived),
	)
	routes := rib.NewRIB()
	require.NoError(t, routes.AddUnicastRoute(netip.MustParsePrefix("203.0.113.0/24"), netip.MustParseAddr("192.0.2.1"), rib.RouteSourceStatic))
	source := operator.NewRouteSource(fixture.Table, gatewayRIBSnapshot{RIB: routes}, operator.WithRouteSourceNeighbours("remote", input))
	entered := make(chan struct{})
	release := make(chan struct{})
	var attempts atomic.Int64
	sink := &gatewayFIBSink{FIBs: make(chan *routepb.UpdateFIBRequest, 16)}
	sink.BeforeUpdate = func(ctx context.Context, request *routepb.UpdateFIBRequest) error {
		if request.GetEntries()[0].GetNexthops()[0].GetDstMac().GetAddr() == 3 && attempts.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
				return status.Error(codes.Unavailable, "gateway was unavailable")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	actuator, functions := newGatewayActuatorFixture(t, sink, operator.WithGatewayActuatorRemoteInput(input))
	chunk := replacementChunk("remote", 100, "192.0.2.1")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, chunk))
	snapshot, available := source.Snapshot()
	require.True(t, available)
	require.NoError(t, actuator.Apply(t.Context(), snapshot))
	baseline := awaitGatewayResult(t, sink.FIBs)
	require.NotEqual(t, uint64(3), baseline.GetEntries()[0].GetNexthops()[0].GetDstMac().GetAddr())
	chunk.Entries[0].LinkAddr = &commonpb.MACAddress{Addr: 3}
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, chunk))
	observed := &observedGatewayActuator{GatewayActuator: actuator, Results: make(chan error, 64)}
	reconciler := commonoperator.NewReconciler(observed, source,
		commonoperator.WithReconcileBackoff(time.Millisecond, time.Second),
		commonoperator.WithReconcileInterval(time.Hour),
	)
	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return reconciler.Run(ctx) })
	t.Cleanup(func() { cancel(); require.ErrorIs(t, group.Wait(), context.Canceled) })
	awaitGatewayResult(t, entered)
	require.Eventually(t, func() bool { return !input.Available() }, 5*time.Second, time.Millisecond)
	_, available = source.Snapshot()
	require.False(t, available)
	close(release)
	require.Error(t, awaitGatewayResult(t, observed.Results))
	require.Error(t, awaitGatewayResult(t, observed.Results))
	require.Empty(t, sink.FIBs)
	require.Equal(t, int64(1), functions.Updates.Load())
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, chunk))
	source.WakeFunc()()
	updated := awaitGatewayResult(t, sink.FIBs)
	require.Equal(t, uint64(3), updated.GetEntries()[0].GetNexthops()[0].GetDstMac().GetAddr())
}

// Test_GatewayActuator_RemoteGeneration verifies that changed input invalidates
// captured work while equivalent heartbeats allow a FIB build to finish.
func Test_GatewayActuator_RemoteGeneration(t *testing.T) {
	for _, test := range []struct {
		name        string
		duringBuild bool
		remove      bool
		heartbeat   bool
	}{
		{name: "replacement before apply"},
		{name: "deletion before apply", remove: true},
		{name: "replacement during FIB build", duringBuild: true},
		{name: "deletion during FIB build", duringBuild: true, remove: true},
		{name: "equivalent heartbeat before apply", heartbeat: true},
		{name: "equivalent heartbeat during FIB build", duringBuild: true, heartbeat: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker := readiness.NewTracker([]readiness.ScopeSpec{{Name: "neighbours"}})
			input := operator.NewNeighbourReadiness("remote", time.Minute, tracker)
			fixture := newNeighbourServiceFixture(t,
				operator.WithNeighbourServiceOnSnapshotReceived(input.OnSnapshotReceived),
				operator.WithNeighbourServiceOnTableRemoved(input.OnTableRemoved),
			)
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("remote", 100)))
			routes := rib.NewRIB()
			source := operator.NewRouteSource(fixture.Table, gatewayRIBSnapshot{RIB: routes}, operator.WithRouteSourceNeighbours("remote", input))
			snapshot, available := source.Snapshot()
			require.True(t, available)
			invalidate := func() {
				if test.remove {
					_, err := fixture.Client.RemoveTable(t.Context(), &operatorpb.RemoveNeighbourTableRequest{Name: "remote"})
					require.NoError(t, err)
				} else if test.heartbeat {
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("remote", 100)))
				} else {
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("remote", 100, "192.0.2.1")))
				}
			}
			options := []operator.GatewayActuatorOption{operator.WithGatewayActuatorRemoteInput(input)}
			if test.duringBuild {
				options = append(options, operator.WithGatewayActuatorOnFIBBuilt(func(string, operator.FIBBuildStats) { invalidate() }))
			} else {
				invalidate()
			}
			sink := &gatewayFIBSink{FIBs: make(chan *routepb.UpdateFIBRequest, 16)}
			actuator, functions := newGatewayActuatorFixture(t, sink, options...)
			err := actuator.Apply(t.Context(), snapshot)
			if test.heartbeat {
				require.NoError(t, err)
				require.Len(t, sink.FIBs, 1)
				require.Equal(t, int64(1), functions.Updates.Load())
			} else {
				require.Error(t, err)
				require.Empty(t, sink.FIBs)
				require.Zero(t, functions.Updates.Load())
			}
		})
	}
}
