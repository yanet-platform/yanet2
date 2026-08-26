// Package counters_test holds regression tests for the counter value-copy
// path implemented in lib/controlplane/agent/counters.c.
package counters_test

import (
	"fmt"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
)

// Memory sizes and topology for the allocation-count regression harness.
const (
	bulkCopyCPSize    = 32 * datasize.MB
	bulkCopyDPSize    = 4 * datasize.MB
	bulkCopyAgentSize = 2 * datasize.MB

	// bulkCopyPipelineCount is chosen so the per-counter allocation term
	// dominates the fixed per-worker overhead: 20 empty pipelines
	// register 6 counters each and the single device storage registers
	// another 12, so the nil tag filter matches 132 counters across 21
	// storages. One worker's set costs one value block per matched
	// counter plus a small tag term per matched storage (one tags array
	// and two strdup'd strings per tag), so a per-worker budget of the
	// matched counters plus eight allocations per storage leaves room
	// for both terms.
	bulkCopyPipelineCount = 20

	// bulkCopyCycles is the number of repeated read cycles the leak
	// guard measures; a cycle that leaks tracked allocations or bytes
	// raises every later cycle's floor above the first cycle's.
	bulkCopyCycles = 10
)

// bulkCopyHarness installs a device and bulkCopyPipelineCount empty
// pipelines at the given worker count and returns the harness's DPConfig.
func bulkCopyHarness(t *testing.T, workerCount uint64) *ffi.DPConfig {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(bulkCopyCPSize),
		DPMemory:      uint64(bulkCopyDPSize),
		WorkerCount:   workerCount,
		Devices:       []string{"port0"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach(
		"bulk-copy-probe", 0, bulkCopyAgentSize,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	input := make([]ffi.DevicePipelineConfig, bulkCopyPipelineCount)
	for idx := range bulkCopyPipelineCount {
		name := fmt.Sprintf("bulk-copy-pipeline-%d", idx)
		require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: name}))
		input[idx] = ffi.DevicePipelineConfig{Name: name, Weight: 1}
	}
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:  "port0",
		Input: input,
	}})
	require.NoError(t, err)

	return h.SharedMemory().DPConfig(0)
}

// bulkCopyProbe runs one CountersByTags call at the given worker count,
// snapshots the wrapped allocator count immediately around it, and
// returns the number of allocations it performed together with the
// number of counters it matched.
func bulkCopyProbe(t *testing.T, workerCount uint64) (allocs uint64, matched int) {
	t.Helper()

	dp := bulkCopyHarness(t, workerCount)

	before := allocCount()
	groups, err := dp.CountersByTags(nil, nil)
	after := allocCount()
	require.NoError(t, err)

	for _, group := range groups {
		matched += len(group.Counters)
	}

	return after - before, matched
}

// TestCountersByTagsPerWorkerAllocationsAreBounded pins the allocation
// shape of the per-worker counter read: every worker's matched set is
// materialized separately, so the total scales once per worker — one
// worker's share stays on the order of one allocation per matched
// counter — and a higher worker count must not grow any single worker's
// share.
func TestCountersByTagsPerWorkerAllocationsAreBounded(t *testing.T) {
	const (
		lowWorkers  = 2
		highWorkers = 8
	)

	lowAllocs, lowMatched := bulkCopyProbe(t, lowWorkers)
	highAllocs, highMatched := bulkCopyProbe(t, highWorkers)

	require.NotZero(t, lowMatched, "probe must match a non-zero counter set")
	require.Equal(t, lowMatched, highMatched,
		"worker count must not change the matched counter set",
	)

	matched := uint64(highMatched)
	storageCount := uint64(bulkCopyPipelineCount + 1)
	perWorkerBudget := matched + 8*storageCount
	perWorkerLow := lowAllocs / lowWorkers
	perWorkerHigh := highAllocs / highWorkers

	require.Less(t, perWorkerHigh, perWorkerBudget,
		"one worker's share must cost one allocation per matched "+
			"counter plus a small tag term per storage",
	)
	require.Less(t, perWorkerHigh, perWorkerLow+matched,
		"a higher worker count must not grow a single worker's share",
	)
	require.GreaterOrEqual(t, perWorkerHigh, matched,
		"a worker's share must average at least one allocation per "+
			"matched counter, or the probe stopped observing the copy",
	)
}

// TestCountersByTagsRepeatedReadsDoNotAccumulate verifies that a full
// read cycle leaves nothing behind: after each call the number of
// outstanding tracked allocations and the bytes they retain return to
// the level the first cycle settled at. A cycle that leaks one of the
// per-worker lists, tag copies, or value blocks leaves its blocks
// outstanding and raises every later cycle's floor.
//
// The probe only observes this binary's own allocations: memory
// allocated inside a precompiled shared library stays invisible here.
func TestCountersByTagsRepeatedReadsDoNotAccumulate(t *testing.T) {
	const workerCount = 4

	dp := bulkCopyHarness(t, workerCount)

	// Prime every lazy one-time allocation the first call makes, so the
	// baseline the later cycles are compared against is steady state.
	_, err := dp.CountersByTags(nil, nil)
	require.NoError(t, err)

	var baseline outstandingAllocs
	var firstDelta uint64
	deltas := make([]uint64, 0, bulkCopyCycles-1)
	for idx := range bulkCopyCycles {
		allocsBefore := allocCount()
		groups, err := dp.CountersByTags(nil, nil)
		after := liveOutstanding()
		require.NoError(t, err)
		require.NotEmpty(t, groups, "read must keep matching counters")

		delta := allocCount() - allocsBefore
		if idx == 0 {
			baseline = after
			firstDelta = delta
			continue
		}
		deltas = append(deltas, delta)
		require.Equal(t, baseline, after,
			"cycle %d left %d allocations outstanding, the first "+
				"cycle settled at %d; a completed read must "+
				"not retain allocations",
			idx, after.count, baseline.count,
		)
	}

	for idx, delta := range deltas {
		require.Equal(t, firstDelta, delta,
			"cycle %d allocated %d, the first cycle allocated %d; "+
				"a repeated read must not change its allocation count",
			idx+1, delta, firstDelta,
		)
	}
}
