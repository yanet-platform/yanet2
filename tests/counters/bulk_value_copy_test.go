// Package counters_test holds regression tests for the counter value-copy
// path implemented in lib/controlplane/agent/agent.c.
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
	// dominates the fixed per-call overhead: 20 empty pipelines register
	// 6 counters each and the single device storage registers another
	// 12, so the nil tag filter matches 132 counters across 21 storages
	// against a fixed term of 4+workerCount+21 allocations (27 at 2
	// workers, 33 at 8).
	bulkCopyPipelineCount = 20
)

// bulkCopyProbe installs a device and bulkCopyPipelineCount empty pipelines
// at the given worker count, snapshots the wrapped allocator count
// immediately around one CountersByTags call, and returns the number of
// allocations it performed together with the number of counters it matched.
func bulkCopyProbe(t *testing.T, workerCount uint64) (allocs uint64, matched int) {
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
	defer h.Free()

	agent, err := h.SharedMemory().AgentAttach(
		"bulk-copy-probe", 0, bulkCopyAgentSize,
	)
	require.NoError(t, err)
	defer func() { _ = agent.CleanUp() }()

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

	dp := h.SharedMemory().DPConfig(0)

	before := allocCount()
	groups, err := dp.CountersByTags(nil, nil)
	after := allocCount()
	require.NoError(t, err)

	for _, group := range groups {
		matched += len(group.Counters)
	}

	return after - before, matched
}

// TestCountersByTagsAllocationsDoNotScaleWithWorkerCount pins the bulk
// value-copy shape of counter_handle_copy_values: reading the same matched
// counter set at a higher worker count must not multiply the allocation
// count by that worker count, and the read overall must cost on the order
// of one allocation per matched counter rather than one per counter per
// worker.
func TestCountersByTagsAllocationsDoNotScaleWithWorkerCount(t *testing.T) {
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
	require.Less(t, highAllocs, lowAllocs+matched,
		"allocation count must not scale with worker count",
	)
	require.Less(t, highAllocs, 2*matched,
		"read must cost on the order of one allocation per counter",
	)
	require.GreaterOrEqual(t, highAllocs, matched,
		"read must average at least one allocation per matched counter, "+
			"or the probe stopped observing the copy",
	)
}
