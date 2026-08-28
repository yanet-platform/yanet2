package counters_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// verifies that a name-based counter read merges the per-worker sets:
// every returned counter carries one value set per dataplane worker.
func Test_DeviceCounters_NameReadSpansEveryWorker(t *testing.T) {
	const workerCount = 4

	dp := bulkCopyHarness(t, workerCount)

	counters := dp.DeviceCounters("port0")
	require.NotEmpty(
		t,
		counters,
		"the device storage must register counters for the device read",
	)
	for _, counter := range counters {
		require.Equalf(
			t,
			workerCount,
			counter.Instances(),
			"counter %q must span one instance per worker",
			counter.Name,
		)
	}
}

// verifies that a name-based read for a device nothing carries yields
// an empty result, not nil or an error path.
func Test_DeviceCounters_UnknownNameIsEmpty(t *testing.T) {
	dp := bulkCopyHarness(t, 2)

	counters := dp.DeviceCounters("no-such-device")
	require.NotNil(t, counters)
	require.Empty(t, counters)
}
