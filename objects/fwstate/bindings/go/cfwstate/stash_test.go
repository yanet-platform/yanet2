package cfwstate_test

import (
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
)

// newMapAgent provides real workers and an agent for idle object operations.
func newMapAgent(t *testing.T) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory: uint64(64 * datasize.MB), DPMemory: uint64(4 * datasize.MB), WorkerCount: 2,
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	},
	)
	require.NoError(t, err)
	t.Cleanup(harness.Free)
	require.NoError(t, harness.InstallEmptyPipeline("idle"))
	agent, err := harness.SharedMemory().AgentAttach("stash", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })
	return harness, agent
}

// Test_MapObjectConfig_AppendLocalEventsPreservesRejectedBatches verifies that
// malformed or overflowing batches cannot partially alter a worker's records.
func Test_MapObjectConfig_AppendLocalEventsPreservesRejectedBatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   cfwstate.Kind
		family byte
	}{
		{name: "IPv4 worker stash", kind: cfwstate.KindV4, family: 4},
		{name: "IPv6 worker stash", kind: cfwstate.KindV6, family: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness, agent := newMapAgent(t)
			object, err := cfwstate.NewMapObjectConfig(agent, "events", tc.kind)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, object.Free()) })
			require.NoError(t, object.CreateMap(cfwstate.MapConfig{
				IndexSize: 16, WorkerCount: 2, StashSize: uint64(2 * cfwstate.StashRecordSize),
			},
			),
			)
			require.NoError(t, harness.BeginRound(0))
			iteration := xerror.Unwrap(harness.SharedMemory().DPConfig(0).WorkerCounters())[0].Iterations
			frame := make([]byte, cfwstate.SyncFrameSize)
			frame[15] = tc.family
			wrongFamily := append([]byte(nil), frame...)
			wrongFamily[15] = 10 - tc.family
			require.Error(t, object.AppendLocalEvents(2, iteration, frame))
			_, err = object.StashLayout(2)
			require.Error(t, err)
			require.Error(t, object.AppendLocalEvents(0, iteration, frame, frame[:10]))
			require.Error(t, object.AppendLocalEvents(0, iteration, frame, wrongFamily))
			require.Error(t, object.AppendLocalEvents(0, iteration, frame, frame, frame))
			require.Empty(t, xerror.Unwrap(object.StashRecords(0)))
			require.NoError(t, object.AppendLocalEvents(0, iteration, frame))
			require.Error(t, object.AppendLocalEvents(0, iteration, frame, frame))
			records := xerror.Unwrap(object.StashRecords(0))
			require.Equal(t, []cfwstate.StashRecord{{Frame: frame, Status: cfwstate.StashRecordPending}}, records)
			require.Empty(t, xerror.Unwrap(object.StashRecords(1)))
			require.NoError(t, harness.BeginRound(0))
			iteration = xerror.Unwrap(harness.SharedMemory().DPConfig(0).WorkerCounters())[0].Iterations
			require.Error(t, object.AppendLocalEvents(0, iteration, wrongFamily))
			require.Equal(t, records, xerror.Unwrap(object.StashRecords(0)))
			require.NoError(t, object.AppendLocalEvents(0, iteration, frame, frame))
			require.Len(t, xerror.Unwrap(object.StashRecords(0)), 2)
		},
		)
	}
}
