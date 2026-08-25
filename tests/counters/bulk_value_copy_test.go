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
	// storages. One worker's set costs on the order of one allocation
	// per matched counter (its value blocks) plus a few per matched
	// storage (its tag copies), so a per-worker budget of twice the
	// matched counter count leaves room for that fixed term.
	bulkCopyPipelineCount = 20

	// bulkCopyCycles is the number of repeated read cycles the leak
	// guard measures; a cycle that leaks counted allocations raises the
	// delta of every later cycle above the first one's.
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
	perWorkerLow := lowAllocs / lowWorkers
	perWorkerHigh := highAllocs / highWorkers

	require.Less(t, perWorkerHigh, 2*matched,
		"one worker's share must cost on the order of one allocation "+
			"per matched counter",
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
// read cycle leaves nothing behind: every counted allocation of a call
// is released by the time the call returns, so repeated cycles allocate
// the same count. A cycle that leaks one of the per-worker lists, tag
// copies, or value blocks raises the delta of every later cycle.
//
// The probe only counts this binary's own malloc/calloc calls, so a leak
// of memory allocated inside libc (such as strdup) stays invisible here.
func TestCountersByTagsRepeatedReadsDoNotAccumulate(t *testing.T) {
	const workerCount = 4

	dp := bulkCopyHarness(t, workerCount)

	deltas := make([]uint64, 0, bulkCopyCycles)
	for range bulkCopyCycles {
		before := allocCount()
		groups, err := dp.CountersByTags(nil, nil)
		after := allocCount()
		require.NoError(t, err)
		require.NotEmpty(t, groups, "read must keep matching counters")
		deltas = append(deltas, after-before)
	}

	for idx, delta := range deltas {
		require.Equal(t, deltas[0], delta,
			"cycle %d allocated %d, the first cycle allocated %d; "+
				"a repeated read must not accumulate allocations",
			idx, delta, deltas[0],
		)
	}
}
