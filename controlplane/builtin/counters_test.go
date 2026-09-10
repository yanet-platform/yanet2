package builtin_test

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/builtin"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// newCountersHarness builds a one-instance in-process dataplane harness
// with the given worker count and registers its teardown. Worker pool
// gauges are seeded at creation from the shared mock pool.
func newCountersHarness(t *testing.T, workers uint64) *dataplaneut.Harness {
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

// An oversized query is refused before any shared memory is touched.
func TestCountersByTagsRejectsOversizedQuery(t *testing.T) {
	svc := builtin.NewCounters(0, nil)

	query := make([]string, 65)
	for idx := range query {
		query[idx] = fmt.Sprintf("counter_%d", idx)
	}

	response, err := svc.ByTags(t.Context(), &ynpb.CountersByTagsRequest{
		Query: query,
	})

	require.Nil(t, response)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// metricByExactLabels finds the metric carrying exactly the given name
// and label set — same names, same values, nothing extra — so a series
// with renamed or dropped labels cannot slip through a subset match.
func metricByExactLabels(
	t *testing.T,
	metrics []*commonpb.Metric,
	name string,
	want map[string]string,
) *commonpb.Metric {
	t.Helper()

	for _, metric := range metrics {
		if metric.GetName() != name {
			continue
		}

		got := make(map[string]string, len(metric.GetLabels()))
		for _, label := range metric.GetLabels() {
			got[label.GetName()] = label.GetValue()
		}
		if maps.Equal(got, want) {
			return metric
		}
	}

	return nil
}

// TestCountersWorkersReportsPoolGauges verifies that the worker snapshot
// carries each worker's pool occupancy as a presence-bearing field with
// the values the dataplane published.
func TestCountersWorkersReportsPoolGauges(t *testing.T) {
	harness := newCountersHarness(t, 2)

	svc := builtin.NewCounters(0, harness.SharedMemory())

	response, err := svc.Workers(t.Context(), &ynpb.WorkerCountersRequest{})

	require.NoError(t, err)
	require.Len(t, response.GetWorkers(), 2)
	for _, worker := range response.GetWorkers() {
		require.NotNil(
			t,
			worker.GetRxMempool(),
			"worker %d pool must be present",
			worker.GetWorkerIdx(),
		)
		require.Equal(t, uint32(1024), worker.GetRxMempool().GetCapacity())
		require.Equal(t, uint32(1024), worker.GetRxMempool().GetAvailable())
	}
}

// TestCountersCollectEmitsPoolGauges verifies that the metrics snapshot
// publishes one capacity and one availability gauge per worker with the
// exact instance-and-worker identity in the labels — both workers, so a
// hardcoded identity fails — while the long-standing worker counters
// keep their original label set.
func TestCountersCollectEmitsPoolGauges(t *testing.T) {
	harness := newCountersHarness(t, 2)

	svc := builtin.NewCounters(0, harness.SharedMemory())

	metrics := svc.Collect()

	// Worker identity (core and queue equal the worker index in the
	// harness), so the two expected label sets differ per worker.
	identity := []map[string]string{
		{
			"instance_id": "0",
			"worker_idx":  "0",
			"core_id":     "0",
			"device_id":   "0",
			"queue_id":    "0",
		},
		{
			"instance_id": "0",
			"worker_idx":  "1",
			"core_id":     "1",
			"device_id":   "0",
			"queue_id":    "1",
		},
	}
	for _, labels := range identity {
		capacity := metricByExactLabels(t, metrics, "worker_rx_mempool_capacity", labels)
		require.NotNil(
			t,
			capacity,
			"capacity gauge with exactly the labels %v is missing",
			labels,
		)
		require.Equal(t, float64(1024), capacity.GetGauge())

		available := metricByExactLabels(t, metrics, "worker_rx_mempool_available", labels)
		require.NotNil(
			t,
			available,
			"availability gauge with exactly the labels %v is missing",
			labels,
		)
		require.Equal(t, float64(1024), available.GetGauge())
	}

	disposed := metricByExactLabels(t, metrics, "worker_disposed", map[string]string{
		"worker_idx": "1",
		"core_id":    "1",
		"device_id":  "0",
		"queue_id":   "1",
	})
	require.NotNil(t, disposed, "existing counters must keep their label set")
}

// TestCountersWorkersRejectsInvalidPoolSnapshot verifies that a pool
// snapshot no healthy dataplane would publish fails the read naming the
// worker instead of rendering a plausible value.
func TestCountersWorkersRejectsInvalidPoolSnapshot(t *testing.T) {
	harness := newCountersHarness(t, 1)
	require.NoError(t, harness.SetWorkerCounter(0, "rx_mempool_available", 5000))

	svc := builtin.NewCounters(0, harness.SharedMemory())

	response, err := svc.Workers(t.Context(), &ynpb.WorkerCountersRequest{})

	require.Nil(t, response)
	require.Error(t, err)
	require.Contains(t, err.Error(), "worker 0")
}

// TestCountersCollectOmitsWorkerFamilyOnInvalidPoolSnapshot verifies
// that an unpublishable pool snapshot removes the whole worker family
// from the metrics snapshot and logs a warning, rather than emitting
// zero-valued gauges or a partial family.
func TestCountersCollectOmitsWorkerFamilyOnInvalidPoolSnapshot(t *testing.T) {
	harness := newCountersHarness(t, 1)
	require.NoError(t, harness.SetWorkerCounter(0, "rx_mempool_capacity", 0))

	core, logs := observer.New(zapcore.WarnLevel)
	svc := builtin.NewCounters(0, harness.SharedMemory(), builtin.WithCountersLog(zap.New(core)))

	metrics := svc.Collect()

	for _, metric := range metrics {
		require.False(
			t,
			strings.HasPrefix(metric.GetName(), "worker_"),
			"%s must be absent from the snapshot",
			metric.GetName(),
		)
	}

	warnings := logs.FilterMessage("failed to collect worker counters")
	require.Equal(t, 1, warnings.Len(), "the family failure must be logged once")
}
