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

// emptyRIBSnapshot provides a route reader with no configured routes.
type emptyRIBSnapshot struct{}

func (m emptyRIBSnapshot) Snapshot() map[string]*rib.RIB { return map[string]*rib.RIB{} }

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
func newReadinessFixture(t *testing.T, maxAge time.Duration) (*neighbourServiceFixture, *operator.NeighbourReadiness, *operator.RouteSource, *readiness.Tracker) {
	t.Helper()
	tracker := readiness.NewTracker([]readiness.ScopeSpec{{Name: "neighbours"}, {Name: "fib:gateway:route0"}})
	observer := operator.NewNeighbourReadiness("remote", maxAge, tracker)
	fixture := newNeighbourServiceFixture(t,
		operator.WithNeighbourServiceReadiness(observer),
		operator.WithNeighbourServiceRemoteSource("remote", []string{"logical0", "logical1"}),
	)
	source := operator.NewRouteSource(fixture.Table, emptyRIBSnapshot{}, operator.WithRouteSourceNeighbours("remote", observer))
	return fixture, observer, source, tracker
}

// Test_NeighbourReadiness_FirstSnapshot verifies that a valid empty snapshot is
// sufficient input while an unobserved source keeps FIB capture blocked.
func Test_NeighbourReadiness_FirstSnapshot(t *testing.T) {
	fixture, observer, source, tracker := newReadinessFixture(t, time.Minute)
	_, ready := source.Snapshot()
	require.False(t, ready)
	require.Equal(t, "SYNCING", neighbourScope(tracker).GetReasons()[0].GetCode())
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("remote", 100)))
	require.True(t, observer.Available())
	_, ready = source.Snapshot()
	require.True(t, ready)
	require.Equal(t, readinesspb.State_STATE_READY, neighbourScope(tracker).GetState())
}

// Test_NeighbourReadiness_WrongTable verifies that another publisher cannot
// unlock the configured remote source.
func Test_NeighbourReadiness_WrongTable(t *testing.T) {
	fixture, observer, source, _ := newReadinessFixture(t, time.Minute)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("other", 100)))
	require.False(t, observer.Available())
	_, ready := source.Snapshot()
	require.False(t, ready)
}

// Test_NeighbourReadiness_EquivalentHeartbeat verifies that fresh equivalent
// commits retain timestamps and generation without waking another build.
func Test_NeighbourReadiness_EquivalentHeartbeat(t *testing.T) {
	fixture, observer, _, _ := newReadinessFixture(t, 200*time.Millisecond)
	input := replacementChunk("remote", 100, "192.0.2.1")
	input.Entries[0].Ifindex = 10
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
	before := sourceEntries(t, fixture.Table, "remote")
	generation, available := observer.Generation()
	require.True(t, available)
	changes := fixture.Changes.Load()
	for range 3 {
		time.Sleep(90 * time.Millisecond)
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
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("remote", 100)))
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
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("remote", 100, "192.0.2.1")))
	before := sourceEntries(t, fixture.Table, "remote")
	invalid := replacementChunk("remote", 100, "192.0.2.2")
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
	input := replacementChunk("remote", 100, "192.0.2.1")
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
	tracker := readiness.NewTracker([]readiness.ScopeSpec{{Name: "neighbours"}})
	observer := operator.NewNeighbourReadiness("remote", time.Minute, tracker)
	fixture := newNeighbourServiceFixture(t,
		operator.WithNeighbourServiceReadiness(observer),
		operator.WithNeighbourServiceRemoteSource("remote", []string{"logical0"}),
	)
	source := operator.NewRouteSource(fixture.Table, emptyRIBSnapshot{}, operator.WithRouteSourceNeighbours("remote", observer))
	input := replacementChunk("remote", 100, "192.0.2.1")
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
type neighbourSnapshotFunc func() neigh.TableSnapshot

func (m neighbourSnapshotFunc) Snapshot() neigh.TableSnapshot { return m() }

// Test_RouteSource_RejectsMixedGenerations verifies that freshness from a later
// replacement cannot authorize an earlier empty, merely recreated table.
func Test_RouteSource_RejectsMixedGenerations(t *testing.T) {
	tracker := readiness.NewTracker([]readiness.ScopeSpec{{Name: "neighbours"}})
	observer := operator.NewNeighbourReadiness("remote", time.Minute, tracker)
	fixture := newNeighbourServiceFixture(t,
		operator.WithNeighbourServiceReadiness(observer),
	)
	input := replacementChunk("remote", 100, "192.0.2.1")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
	intervened := false
	reader := neighbourSnapshotFunc(func() neigh.TableSnapshot {
		if intervened {
			return fixture.Table.Snapshot()
		}
		intervened = true
		_, err := fixture.Client.RemoveTable(t.Context(), &operatorpb.RemoveNeighbourTableRequest{Name: "remote"})
		require.NoError(t, err)
		_, err = fixture.Client.CreateTable(t.Context(), &operatorpb.CreateNeighbourTableRequest{Name: "remote", DefaultPriority: 100})
		require.NoError(t, err)
		captured := fixture.Table.Snapshot()
		require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, input))
		return captured
	})
	source := operator.NewRouteSource(reader, emptyRIBSnapshot{}, operator.WithRouteSourceNeighbours("remote", observer))
	_, ready := source.Snapshot()
	require.False(t, ready)
	snapshot, ready := source.Snapshot()
	require.True(t, ready)
	_, count := snapshot.Neighbours.ViewByDevices(nil).Entries()
	require.Equal(t, 1, count)
}

// Test_NeighbourService_InconsistentRemoteScope verifies that a complete stream
// cannot bind one index to two devices or one device to two observed indices.
func Test_NeighbourService_InconsistentRemoteScope(t *testing.T) {
	for _, sameDevice := range []bool{false, true} {
		fixture := newNeighbourServiceFixture(t, operator.WithNeighbourServiceRemoteSource("remote", []string{"logical0", "logical1"}))
		first := replacementChunk("remote", 100, "192.0.2.1")
		first.Entries[0].Ifindex = 10
		require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, first))
		before := sourceEntries(t, fixture.Table, "remote")
		second := replacementChunk("remote", 100, "192.0.2.2")
		second.Entries[0].Ifindex = 10
		if sameDevice {
			second.Entries[0].Ifindex = 20
		} else {
			second.Entries[0].Device = "logical1"
		}
		require.Error(t, sendNeighbourSnapshot(t.Context(), fixture.Client, first, second))
		require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
	}
}

// Test_Config_RemoteNeighbourContract verifies that remote input requires one
// explicit device owner, a positive age budget and disabled local monitoring.
func Test_Config_RemoteNeighbourContract(t *testing.T) {
	for _, test := range []struct {
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
		t.Run(test.name, func(t *testing.T) {
			config := operator.DefaultConfig()
			config.Gateways = []commonoperator.GatewayConfig{{Name: "first"}}
			config.NetlinkMonitor.Disabled = true
			config.Readiness.RemoteNeighbourTable = "remote"
			config.GatewayDevices = map[string][]string{"first": {"logical0"}}
			require.NoError(t, config.Validate())
			test.mutate(config)
			require.Error(t, config.Validate())
		})
	}
}
