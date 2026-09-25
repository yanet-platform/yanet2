package counters_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Test_CountersByTags_ProbeIgnoresForeignAllocations verifies that completed
// foreign allocator traffic cannot change the cost of a public read.
func Test_CountersByTags_ProbeIgnoresForeignAllocations(t *testing.T) {
	config := bulkCopyHarness(t, 2)
	t.Cleanup(func() { require.Zero(t, probeResetControls()) })
	probeArmRead()
	before := probeSnapshot()
	groups, err := config.CountersByTags(nil, nil)
	after := probeSnapshot()
	ordinary := after.allocations - before.allocations
	requireProbeComplete(t, before, after)
	require.Equal(t, before.outstanding, after.outstanding)
	require.NoError(t, err)
	require.NotEmpty(t, groups)
	require.NotZero(t, ordinary)
	completed := after.noiseCompleted
	probeArmNoise()
	probeArmRead()
	before = probeSnapshot()
	noisyGroups, err := config.CountersByTags(nil, nil)
	after = probeSnapshot()
	noisy := after.allocations - before.allocations
	requireProbeComplete(t, before, after)
	require.Equal(t, before.outstanding, after.outstanding)
	require.NoError(t, err)
	require.Equal(t, completed+3, after.noiseCompleted)
	require.Equal(t, groups, noisyGroups)
	require.Equal(t, ordinary, noisy)
}

// Test_CountersByTags_ProbeUnknownFree verifies that unowned pointers and NULL
// do not disturb the accounting of a completed public read.
func Test_CountersByTags_ProbeUnknownFree(t *testing.T) {
	config := bulkCopyHarness(t, 2)
	probeArmRead()
	before := probeSnapshot()
	groups, err := config.CountersByTags(nil, nil)
	after := probeSnapshot()
	require.NoError(t, err)
	require.NotEmpty(t, groups)
	requireProbeComplete(t, before, after)
	require.Equal(t, before.outstanding, after.outstanding)
	probeForeignNoise()
	foreign := probeSnapshot()
	requireProbeComplete(t, foreign)
	require.Equal(t, after.allocations, foreign.allocations)
	require.Equal(t, after.outstanding, foreign.outstanding)
	require.Equal(t, after.noiseCompleted+1, foreign.noiseCompleted)
}

// Test_CountersByTags_ProbeRetainedRoot verifies that an escaped allocation
// remains owned until actual cleanup, even on another thread.
func Test_CountersByTags_ProbeRetainedRoot(t *testing.T) {
	const workerCount = 2
	config := bulkCopyHarness(t, workerCount)
	t.Cleanup(func() {
		require.Zero(t, probeReleaseRetained(false))
		require.Zero(t, probeResetControls())
	})
	before := probeSnapshot()
	probeArmRetention()
	probeArmRead()
	groups, err := config.CountersByTags(nil, nil)
	retained := probeSnapshot()
	require.NoError(t, err)
	require.NotEmpty(t, groups)
	requireProbeComplete(t, before, retained)
	require.Equal(t, before.suppressed+1, retained.suppressed)
	require.Equal(t, before.outstanding.count+1, retained.outstanding.count)
	require.Equal(t, probeRootBytes(workerCount),
		retained.outstanding.bytes-before.outstanding.bytes,
	)
	require.NotZero(t, probeResetControls(), "cannot reset live ownership")
	probeForeignNoise()
	require.Equal(t, retained.outstanding, probeSnapshot().outstanding)
	require.Zero(t, probeReleaseRetained(true))
	after := probeSnapshot()
	requireProbeComplete(t, after)
	require.Equal(t, before.outstanding, after.outstanding)
	require.Zero(t, probeReleaseRetained(true), "cleanup must be idempotent")
	require.Equal(t, after, probeSnapshot())
}

// Test_CountersByTags_ProbeErrorRestoresScope verifies that a C-side rejection
// releases ownership and restores admission before same-thread foreign calls.
func Test_CountersByTags_ProbeErrorRestoresScope(t *testing.T) {
	config := bulkCopyHarness(t, 2)
	t.Cleanup(func() { require.Zero(t, probeResetControls()) })
	tags := make([]ffi.CounterTag, 17)
	for idx := range tags {
		tags[idx] = ffi.CounterTag{Key: fmt.Sprintf("key%d", idx), Value: "value"}
	}
	probeArmRead()
	before := probeSnapshot()
	_, err := config.CountersByTags(tags, nil)
	after := probeSnapshot()
	require.Error(t, err)
	requireProbeComplete(t, before, after)
	require.Equal(t, before.outstanding, after.outstanding)
	ordinary := after.allocations - before.allocations
	probeArmNoise()
	probeArmRead()
	before = probeSnapshot()
	_, err = config.CountersByTags(tags, nil)
	after = probeSnapshot()
	require.Error(t, err)
	requireProbeComplete(t, before, after)
	require.Equal(t, before.noiseCompleted+3, after.noiseCompleted)
	require.Equal(t, ordinary, after.allocations-before.allocations)
	require.Equal(t, before.outstanding, after.outstanding)
	probeForeignNoise()
	foreign := probeSnapshot()
	requireProbeComplete(t, foreign)
	require.Equal(t, after.allocations, foreign.allocations)
	require.Equal(t, after.outstanding, foreign.outstanding)
	probeArmRead()
	groups, err := config.CountersByTags(nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, groups)
	requireProbeComplete(t, probeSnapshot())
}

// Test_CountersByTags_ProbeOverflowReported verifies that incomplete ownership
// remains visible after a successful read until explicitly acknowledged.
func Test_CountersByTags_ProbeOverflowReported(t *testing.T) {
	config := bulkCopyHarness(t, 2)
	t.Cleanup(func() { require.Zero(t, probeResetControls()) })
	before := probeSnapshot()
	requireProbeComplete(t, before)
	probeArmOverflow()
	probeArmRead()
	groups, err := config.CountersByTags(nil, nil)
	after := probeSnapshot()
	require.NoError(t, err)
	require.NotEmpty(t, groups)
	require.True(t, after.incomplete)
	require.Zero(t, after.hookError)
	require.Equal(t, before.outstanding, after.outstanding)
	probeArmRead()
	_, err = config.CountersByTags(nil, nil)
	require.NoError(t, err)
	require.True(t, probeSnapshot().incomplete, "overflow must be sticky")
	require.Zero(t, probeResetControls())
	probeArmRead()
	before = probeSnapshot()
	_, err = config.CountersByTags(nil, nil)
	after = probeSnapshot()
	require.NoError(t, err)
	requireProbeComplete(t, before, after)
	require.Greater(t, after.allocations, before.allocations)
	require.Equal(t, before.outstanding, after.outstanding)
}

// requireProbeComplete rejects measurements with missing ownership records.
func requireProbeComplete(t *testing.T, snapshots ...allocationSnapshot) {
	t.Helper()
	for _, snapshot := range snapshots {
		require.False(t, snapshot.incomplete, "probe ownership registry overflowed")
		require.Zero(t, snapshot.hookError, "probe hook failed")
	}
}
