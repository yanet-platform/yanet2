package ffi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
)

// newWorkerCountersHarness builds a one-instance in-process dataplane
// harness with the given worker count and registers its teardown. Every
// worker shares the harness mock pool, whose gauges are seeded at
// creation.
func newWorkerCountersHarness(t *testing.T, workers uint64) *dataplaneut.Harness {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:    1 << 25,
		DPMemory:    1 << 20,
		WorkerCount: workers,
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	return harness
}

// TestWorkerCountersReportsPoolGauges verifies that every worker's
// snapshot carries the pool occupancy published by the dataplane: the
// mock pool's full capacity and its current availability.
func TestWorkerCountersReportsPoolGauges(t *testing.T) {
	harness := newWorkerCountersHarness(t, 2)

	dpConfig := harness.SharedMemory().DPConfig(0)
	workers, err := dpConfig.WorkerCounters()
	require.NoError(t, err)
	require.Len(t, workers, 2)

	for idx, worker := range workers {
		require.Equal(t, idx, int(worker.WorkerIdx))
		require.NotNil(t, worker.RxMempool, "worker %d pool must be present", idx)
		require.Equal(t, uint32(1024), worker.RxMempool.Capacity)
		require.Equal(t, uint32(1024), worker.RxMempool.Available)
	}
}

// TestWorkerCountersRejectsZeroCapacity verifies that a pool reported
// with no capacity fails the read naming the worker rather than
// surfacing a zero-valued pool.
func TestWorkerCountersRejectsZeroCapacity(t *testing.T) {
	harness := newWorkerCountersHarness(t, 1)
	require.NoError(t, harness.SetWorkerCounter(0, "rx_mempool_capacity", 0))

	dpConfig := harness.SharedMemory().DPConfig(0)
	workers, err := dpConfig.WorkerCounters()

	require.Nil(t, workers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "worker 0")
	require.Contains(t, err.Error(), "capacity")
}

// TestWorkerCountersRejectsAvailabilityAboveCapacity verifies that an
// availability exceeding the capacity is refused instead of being
// clamped into a plausible value.
func TestWorkerCountersRejectsAvailabilityAboveCapacity(t *testing.T) {
	harness := newWorkerCountersHarness(t, 1)
	require.NoError(t, harness.SetWorkerCounter(0, "rx_mempool_available", 5000))

	dpConfig := harness.SharedMemory().DPConfig(0)
	workers, err := dpConfig.WorkerCounters()

	require.Nil(t, workers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "worker 0")
	require.Contains(t, err.Error(), "exceeds capacity")
}

// TestWorkerCountersRejectsOversizedCapacity verifies that a capacity
// beyond the mempool object width is refused rather than truncated into
// a wrong pool size.
func TestWorkerCountersRejectsOversizedCapacity(t *testing.T) {
	harness := newWorkerCountersHarness(t, 1)
	require.NoError(t, harness.SetWorkerCounter(0, "rx_mempool_capacity", 1<<32))

	dpConfig := harness.SharedMemory().DPConfig(0)
	workers, err := dpConfig.WorkerCounters()

	require.Nil(t, workers)
	require.Error(t, err)
	require.Contains(t, err.Error(), "worker 0")
	require.Contains(t, err.Error(), "capacity")
}
