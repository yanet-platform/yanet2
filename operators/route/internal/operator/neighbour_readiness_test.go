package operator_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// neighbourScope reads the real tracker without depending on observer internals.
func neighbourScope(tracker *readiness.Tracker) *readinesspb.Scope {
	for _, scope := range tracker.Ready(&readinesspb.ReadyRequest{}).GetScopes() {
		if scope.GetName() == "neighbours" {
			return scope
		}
	}
	return nil
}

// newReadinessFixture connects real snapshot commits to freshness and FIB capture.
func newReadinessFixture(t *testing.T, maxAge time.Duration, options ...operator.NeighbourServiceOption) (*neighbourServiceFixture, *operator.NeighbourReadiness, *operator.RouteSource, *readiness.Tracker) {
	t.Helper()
	tracker := readiness.NewTracker([]readiness.ScopeSpec{{Name: "neighbours"}, {Name: "fib:gateway:route0"}})
	observer := operator.NewNeighbourReadiness("remote", maxAge, tracker)
	options = append([]operator.NeighbourServiceOption{
		operator.WithNeighbourServiceReadiness(observer),
		operator.WithNeighbourServiceRemoteSource("remote", []string{"logical0", "logical1"}),
	}, options...)
	fixture := newNeighbourServiceFixture(t, options...)
	source := operator.NewRouteSource(fixture.Table, gatewayRIBSnapshot{RIB: rib.NewRIB()}, operator.WithRouteSourceRemoteInput(observer))
	return fixture, observer, source, tracker
}

// Test_NeighbourReadiness_FirstSnapshot verifies that a valid empty snapshot is
// sufficient input while an unobserved source keeps FIB capture blocked.
func Test_NeighbourReadiness_FirstSnapshot(t *testing.T) {
	fixture, observer, source, tracker := newReadinessFixture(t, time.Minute)
	_, ready := source.Snapshot()
	require.False(t, ready)
	require.Equal(t, "SYNCING", neighbourScope(tracker).GetReasons()[0].GetCode())
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100)))
	require.True(t, observer.Available())
	_, ready = source.Snapshot()
	require.True(t, ready)
	require.Equal(t, readinesspb.State_STATE_READY, neighbourScope(tracker).GetState())
}

// Test_NeighbourReadiness_WrongTable verifies that another publisher cannot
// unlock the configured remote source.
func Test_NeighbourReadiness_WrongTable(t *testing.T) {
	fixture, observer, source, _ := newReadinessFixture(t, time.Minute)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("other", 100)))
	require.False(t, observer.Available())
	_, ready := source.Snapshot()
	require.False(t, ready)
}

// Test_NeighbourReadiness_EquivalentHeartbeat verifies that fresh equivalent
// reordered commits refresh input age but retain timestamps and generation
// without waking another build.
func Test_NeighbourReadiness_EquivalentHeartbeat(t *testing.T) {
	fixture, observer, _, _ := newReadinessFixture(t, 200*time.Millisecond)
	input := replacementRequest("remote", 100, "192.0.2.1", "2001:db8::1")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
	before := sourceEntries(t, fixture.Table, "remote")
	generation, available := observer.Generation()
	require.True(t, available)
	changes := fixture.Changes.Load()
	for range 3 {
		time.Sleep(90 * time.Millisecond)
		input.Entries[0], input.Entries[1] = input.Entries[1], input.Entries[0]
		require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
		current, available := observer.Generation()
		require.True(t, available)
		require.Equal(t, generation, current)
		require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
		require.Equal(t, changes, fixture.Changes.Load())
	}
}

// Test_NeighbourReadiness_FIBFailure verifies that input health is independent
// of the result of installing a FIB on the gateway.
func Test_NeighbourReadiness_FIBFailure(t *testing.T) {
	fixture, observer, _, tracker := newReadinessFixture(t, time.Minute)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100)))
	tracker.Set("fib:gateway:route0", readinesspb.State_STATE_NOT_READY)
	require.True(t, observer.Available())
	require.Equal(t, readinesspb.State_STATE_READY, neighbourScope(tracker).GetState())
}

// Test_NeighbourReadiness_Expiry verifies that silence expires input even when
// rejected replacements arrive, preserving the last committed diagnostics.
func Test_NeighbourReadiness_Expiry(t *testing.T) {
	fixture, observer, source, tracker := newReadinessFixture(t, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- observer.Run(ctx) }()
	t.Cleanup(func() { cancel(); require.ErrorIs(t, <-done, context.Canceled) })
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100, "192.0.2.1")))
	before := sourceEntries(t, fixture.Table, "remote")
	invalid := replacementRequest("remote", 100, "192.0.2.2")
	invalid.Entries[0].Device = "unknown"
	require.Error(t, sendNeighbourSnapshot(t.Context(), fixture.Client, invalid))
	require.Eventually(t, func() bool {
		reasons := neighbourScope(tracker).GetReasons()
		return !observer.Available() && len(reasons) == 1 && reasons[0].GetCode() == "STALE"
	}, time.Second, time.Millisecond)
	_, ready := source.Snapshot()
	require.False(t, ready)
	require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
	require.Error(t, sendNeighbourSnapshot(t.Context(), fixture.Client, invalid))
	require.False(t, observer.Available())
}

// Test_NeighbourReadiness_RecoveryWake verifies that an equivalent snapshot
// after expiry wakes pending work without changing its content generation.
func Test_NeighbourReadiness_RecoveryWake(t *testing.T) {
	fixture, observer, source, tracker := newReadinessFixture(t, 100*time.Millisecond)
	input := replacementRequest("remote", 100, "192.0.2.1")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
	generation, _ := observer.Generation()
	changes := fixture.Changes.Load()
	require.Eventually(t, func() bool { return !observer.Available() }, time.Second, time.Millisecond)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
	current, available := observer.Generation()
	require.True(t, available)
	require.Equal(t, generation, current)
	require.Equal(t, changes+1, fixture.Changes.Load())
	_, ready := source.Snapshot()
	require.True(t, ready)
	require.Equal(t, readinesspb.State_STATE_READY, neighbourScope(tracker).GetState())
}

// Test_NeighbourReadiness_TableLifecycle verifies that deletion invalidates
// old freshness and incremental additions cannot bypass remote input validation.
func Test_NeighbourReadiness_TableLifecycle(t *testing.T) {
	fixture, observer, source, _ := newReadinessFixture(t, time.Minute)
	input := replacementRequest("remote", 100, "192.0.2.1")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
	_, err := fixture.Client.UpdateNeighbours(t.Context(), &operatorpb.UpdateNeighboursRequest{Table: "remote", Entries: input.Entries})
	require.Error(t, err)
	_, err = fixture.Client.RemoveTable(t.Context(), &operatorpb.RemoveNeighbourTableRequest{Name: "remote"})
	require.NoError(t, err)
	require.False(t, observer.Available())
	_, err = fixture.Client.CreateTable(t.Context(), &operatorpb.CreateNeighbourTableRequest{Name: "remote", DefaultPriority: 100})
	require.NoError(t, err)
	_, ready := source.Snapshot()
	require.False(t, ready)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
	_, ready = source.Snapshot()
	require.True(t, ready)
}

// neighbourSnapshotFunc injects an intervening table generation during capture.
type neighbourSnapshotFunc func() neigh.NexthopCacheView

func (m neighbourSnapshotFunc) View() neigh.NexthopCacheView { return m() }

// Test_RouteSource_CaptureFreshness verifies that content changes, removal and
// expiry during view capture reject work, while an equivalent refresh is safe.
//
// A newly authorized replacement must not validate an earlier empty view
// captured between table recreation and the complete replacement.
func Test_RouteSource_CaptureFreshness(t *testing.T) {
	for _, action := range []string{"replace", "clear", "remove", "recreate", "expire", "heartbeat"} {
		t.Run(action, func(t *testing.T) {
			fixture, input, _, _ := newReadinessFixture(t, 100*time.Millisecond)
			request := replacementRequest("remote", 100, "192.0.2.1")
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, request))
			intervened := false
			reader := neighbourSnapshotFunc(func() neigh.NexthopCacheView {
				captured := fixture.Table.View()
				if intervened {
					return captured
				}
				intervened = true
				switch action {
				case "replace":
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100, "192.0.2.2")))
				case "clear":
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100)))
				case "remove", "recreate":
					_, err := fixture.Client.RemoveTable(t.Context(), &operatorpb.RemoveNeighbourTableRequest{Name: "remote"})
					require.NoError(t, err)
					if action == "recreate" {
						_, err = fixture.Client.CreateTable(t.Context(), &operatorpb.CreateNeighbourTableRequest{Name: "remote", DefaultPriority: 100})
						require.NoError(t, err)
						captured = fixture.Table.View()
						require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, request))
					}
				case "expire":
					require.Eventually(t, func() bool { return !input.Available() }, time.Second, time.Millisecond)
				case "heartbeat":
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, request))
				}
				return captured
			})
			source := operator.NewRouteSource(reader, gatewayRIBSnapshot{RIB: rib.NewRIB()}, operator.WithRouteSourceRemoteInput(input))
			_, available := source.Snapshot()
			require.Equal(t, action == "heartbeat", available)
			snapshot, available := source.Snapshot()
			require.Equal(t, action != "remove" && action != "expire", available)
			if available {
				_, count := snapshot.Neighbours.Entries()
				wanted := 1
				if action == "clear" {
					wanted = 0
				}
				require.Equal(t, wanted, count)
			}
		})
	}
}

// Test_Config_RemoteNeighbourContract verifies that remote input requires one
// explicit device owner, a positive age budget and disabled local monitoring.
func Test_Config_RemoteNeighbourContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*operator.Config)
	}{
		{name: "local monitor enabled", mutate: func(config *operator.Config) { config.NetlinkMonitor.Disabled = false }},
		{name: "missing budget", mutate: func(config *operator.Config) { config.Readiness.RemoteNeighbourMaxAge = 0 }},
		{name: "builtin source", mutate: func(config *operator.Config) { config.Readiness.RemoteNeighbourTable = "static" }},
		{name: "wildcard ownership", mutate: func(config *operator.Config) { config.GatewayDevices = nil }},
		{name: "empty device", mutate: func(config *operator.Config) { config.GatewayDevices["first"] = []string{""} }},
		{name: "duplicate owner", mutate: func(config *operator.Config) {
			config.Gateways = append(config.Gateways, commonoperator.GatewayConfig{Name: "second"})
			config.GatewayDevices["second"] = []string{"logical0"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := operator.DefaultConfig()
			config.Gateways = []commonoperator.GatewayConfig{{Name: "first"}}
			config.NetlinkMonitor.Disabled = true
			config.Readiness.RemoteNeighbourTable = "remote"
			config.GatewayDevices = map[string][]string{"first": {"logical0"}}
			require.NoError(t, config.Validate())
			tc.mutate(config)
			require.Error(t, config.Validate())
		})
	}
}
