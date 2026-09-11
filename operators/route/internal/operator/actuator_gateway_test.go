package operator_test

import (
	"context"
	"net/netip"
	"sync"
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
	server := grpc.NewServer()
	functions := &gatewayFunctionSink{}
	routepb.RegisterRouteServiceServer(server, sink)
	ynpb.RegisterFunctionServiceServer(server, functions)
	endpoint := serveTestGRPCServer(t, server)
	options = append(options, operator.WithGatewayActuatorFunction(operator.FunctionConfig{
		Name: xcfg.MustNonEmptyString("fn:route"), Chain: xcfg.MustNonEmptyString("default"),
		Module: xcfg.MustNonEmptyString("route0"), Weight: 1,
	}))
	actuator, err := operator.NewGatewayActuator(commonoperator.GatewayConfig{
		Name: "gateway", Endpoint: xcfg.MustNonEmptyString(endpoint),
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

// Test_GatewayActuator_PartialFailure verifies that invalid module names and
// failed writes remain errors without skipping valid modules or the function.
func Test_GatewayActuator_PartialFailure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		unnamed     bool
		writeError  bool
		wantMessage string
	}{
		{name: "unnamed module", unnamed: true, wantMessage: "FIB is missing module config name"},
		{name: "failed gateway write", writeError: true, wantMessage: "failed to push FIB to gateway \"gateway\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newNeighbourServiceFixture(t)
			source := operator.NewRouteSource(fixture.Table, gatewayRIBSnapshot{RIB: rib.NewRIB()})
			snapshot, available := source.Snapshot()
			require.True(t, available)
			if tc.unnamed {
				snapshot.RIBs[""] = snapshot.RIBs["route0"]
			}
			sink := &gatewayFIBSink{FIBs: make(chan *routepb.UpdateFIBRequest, 1)}
			if tc.writeError {
				sink.BeforeUpdate = func(ctx context.Context, request *routepb.UpdateFIBRequest) error {
					return status.Error(codes.Unavailable, "gateway write failed")
				}
			}
			actuator, functions := newGatewayActuatorFixture(t, sink)
			err := actuator.Apply(t.Context(), snapshot)
			require.ErrorContains(t, err, tc.wantMessage)
			if tc.writeError {
				require.Empty(t, sink.FIBs)
			} else {
				require.Len(t, sink.FIBs, 1)
				require.Equal(t, "route0", awaitGatewayResult(t, sink.FIBs).GetModuleName())
			}
			require.Equal(t, int64(1), functions.Updates.Load())
		})
	}
}

// Test_GatewayActuator_StaleRetry verifies that a new retry after input expiry
// preserves the installed FIB until a fresh complete snapshot arrives.
func Test_GatewayActuator_StaleRetry(t *testing.T) {
	fixture, input, _, _ := newReadinessFixture(t, 250*time.Millisecond)
	routes := rib.NewRIB()
	require.NoError(t, routes.AddUnicastRoute(netip.MustParsePrefix("203.0.113.0/24"), netip.MustParseAddr("192.0.2.1"), rib.RouteSourceStatic))
	source := operator.NewRouteSource(fixture.Table, gatewayRIBSnapshot{RIB: routes}, operator.WithRouteSourceRemoteInput(input))
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
	chunk := replacementRequest("remote", 100, "192.0.2.1")
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
	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			fixture, input, source, _ := newReadinessFixture(t, time.Minute)
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100)))
			snapshot, available := source.Snapshot()
			require.True(t, available)
			invalidate := func() {
				if tc.remove {
					_, err := fixture.Client.RemoveTable(t.Context(), &operatorpb.RemoveNeighbourTableRequest{Name: "remote"})
					require.NoError(t, err)
				} else if tc.heartbeat {
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100)))
				} else {
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100, "192.0.2.1")))
				}
			}
			options := []operator.GatewayActuatorOption{operator.WithGatewayActuatorRemoteInput(input)}
			if tc.duringBuild {
				options = append(options, operator.WithGatewayActuatorOnFIBBuilt(func(string, operator.FIBBuildStats) { invalidate() }))
			} else {
				invalidate()
			}
			sink := &gatewayFIBSink{FIBs: make(chan *routepb.UpdateFIBRequest, 16)}
			actuator, functions := newGatewayActuatorFixture(t, sink, options...)
			err := actuator.Apply(t.Context(), snapshot)
			if tc.heartbeat {
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

// Test_GatewayActuator_CommitBeforeNotification verifies that committed data
// already invalidates old FIB work while the receiver notification is paused.
func Test_GatewayActuator_CommitBeforeNotification(t *testing.T) {
	for _, operation := range []string{"replacement", "removal"} {
		t.Run(operation, func(t *testing.T) {
			committed := make(chan struct{})
			release := make(chan struct{})
			resume := sync.OnceFunc(func() { close(release) })
			var changes atomic.Int32
			fixture, input, source, _ := newReadinessFixture(t, time.Minute,
				operator.WithNeighbourServiceOnChanged(func() {
					if changes.Add(1) == 2 {
						close(committed)
						<-release
					}
				}),
			)
			t.Cleanup(resume)
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client,
				replacementRequest("remote", 100, "192.0.2.1"),
			))
			previous, available := source.Snapshot()
			require.True(t, available)
			result := make(chan error, 1)
			go func() {
				if operation == "removal" {
					_, err := fixture.Client.RemoveTable(t.Context(), &operatorpb.RemoveNeighbourTableRequest{Name: "remote"})
					result <- err
				} else {
					result <- sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100, "192.0.2.2"))
				}
			}()
			awaitGatewayResult(t, committed)
			current, available := input.Generation()
			require.NotEqual(t, previous.NeighbourGeneration, current)
			require.Equal(t, operation == "replacement", available)
			captured, available := source.Snapshot()
			require.Equal(t, operation == "replacement", available)
			if available {
				require.Equal(t, current, captured.NeighbourGeneration)
				entry, found := captured.Neighbours.Lookup(netip.MustParseAddr("192.0.2.2"))
				require.True(t, found)
				require.Equal(t, netip.MustParseAddr("192.0.2.2"), entry.NextHop)
			}
			sink := &gatewayFIBSink{FIBs: make(chan *routepb.UpdateFIBRequest, 1)}
			actuator, functions := newGatewayActuatorFixture(t, sink, operator.WithGatewayActuatorRemoteInput(input))
			require.Error(t, actuator.Apply(t.Context(), previous))
			require.Empty(t, sink.FIBs)
			require.Zero(t, functions.Updates.Load())
			resume()
			require.NoError(t, awaitGatewayResult(t, result))
		})
	}
}

// Test_GatewayActuator_GenerationRetry verifies that a failed FIB write cannot
// retry captured work after replacement or removal of its authorizing input.
func Test_GatewayActuator_GenerationRetry(t *testing.T) {
	for _, operation := range []string{"replacement", "removal"} {
		t.Run(operation, func(t *testing.T) {
			fixture, input, source, _ := newReadinessFixture(t, time.Minute)
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100, "192.0.2.1")))
			snapshot, available := source.Snapshot()
			require.True(t, available)
			entered, release := make(chan struct{}), make(chan struct{})
			resume := sync.OnceFunc(func() { close(release) })
			var attempts atomic.Int32
			sink := &gatewayFIBSink{FIBs: make(chan *routepb.UpdateFIBRequest, 1)}
			sink.BeforeUpdate = func(ctx context.Context, request *routepb.UpdateFIBRequest) error {
				if attempts.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
						return status.Error(codes.Unavailable, "write failed")
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}
			actuator, _ := newGatewayActuatorFixture(t, sink, operator.WithGatewayActuatorRemoteInput(input))
			t.Cleanup(resume)
			result := make(chan error, 1)
			go func() { result <- actuator.Apply(t.Context(), snapshot) }()
			awaitGatewayResult(t, entered)
			if operation == "removal" {
				_, err := fixture.Client.RemoveTable(t.Context(), &operatorpb.RemoveNeighbourTableRequest{Name: "remote"})
				require.NoError(t, err)
			} else {
				require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100, "192.0.2.2")))
			}
			resume()
			require.Error(t, awaitGatewayResult(t, result))
			for range 2 {
				require.Error(t, actuator.Apply(t.Context(), snapshot))
			}
			require.Equal(t, int32(1), attempts.Load())
			require.Empty(t, sink.FIBs)
		})
	}
}
