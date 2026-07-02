package operator

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

// findMetric returns the metric named name whose label set matches labels
// exactly (order-independent), or nil if none matches.
func findMetric(metricList []*commonpb.Metric, name string, labels map[string]string) *commonpb.Metric {
	for _, metric := range metricList {
		if metric.GetName() != name {
			continue
		}

		if len(metric.GetLabels()) != len(labels) {
			continue
		}

		matched := true
		for _, label := range metric.GetLabels() {
			if labels[label.GetName()] != label.GetValue() {
				matched = false
				break
			}
		}
		if matched {
			return metric
		}
	}

	return nil
}

// fakeActuator is a minimal Actuator used to drive meteredActuator in
// isolation from the real gateway RPC client.
type fakeActuator struct {
	applyErr   error
	applyCalls int
	closeErr   error
	closeCalls int
}

func (m *fakeActuator) Apply(ctx context.Context, snapshot RouteSnapshot) error {
	m.applyCalls++
	return m.applyErr
}

func (m *fakeActuator) Close() error {
	m.closeCalls++
	return m.closeErr
}

// TestMetrics_ReconcileEvents verifies that reconcile-loop observer methods
// update the corresponding counters and gauges rendered by Collect.
func TestMetrics_ReconcileEvents(t *testing.T) {
	m := NewMetrics(newRIBStore(zap.NewNop()), false)

	m.OnReconcileCompleted(nil)
	m.OnReconcileCompleted(errors.New("boom"))
	m.OnBackoffScheduled(2 * time.Second)
	m.OnStateChanged(operator.ReconcilerStateApplying)

	metricList := m.Collect()

	total := findMetric(metricList, "route_operator_reconcile_total", map[string]string{})
	require.NotNil(t, total)
	require.Equal(t, uint64(2), total.GetCounter())

	errs := findMetric(metricList, "route_operator_reconcile_errors_total", map[string]string{})
	require.NotNil(t, errs)
	require.Equal(t, uint64(1), errs.GetCounter())

	backoff := findMetric(metricList, "route_operator_backoff_seconds", map[string]string{})
	require.NotNil(t, backoff)
	require.Equal(t, 2.0, backoff.GetGauge())

	applying := findMetric(metricList, "route_operator_state", map[string]string{"state": "applying"})
	require.NotNil(t, applying)
	require.Equal(t, 1.0, applying.GetGauge())

	idle := findMetric(metricList, "route_operator_state", map[string]string{"state": "idle"})
	require.NotNil(t, idle)
	require.Equal(t, 0.0, idle.GetGauge())

	m.OnBackoffReset()
	backoff = findMetric(m.Collect(), "route_operator_backoff_seconds", map[string]string{})
	require.NotNil(t, backoff)
	require.Equal(t, 0.0, backoff.GetGauge())
}

// TestMetrics_RIBFeedEvents verifies that RIB session and update callbacks
// are rendered per module, without leaking into other modules' counters.
func TestMetrics_RIBFeedEvents(t *testing.T) {
	m := NewMetrics(newRIBStore(zap.NewNop()), false)

	m.OnRIBSessionStart("route0", 1)
	m.OnRIBSessionStart("route0", 2)
	m.OnRIBSessionEnd("route0", 1)
	m.OnRIBSessionStart("route1", 1)
	m.OnRIBUpdate(3)
	m.OnRIBUpdate(2)

	metricList := m.Collect()

	starts0 := findMetric(metricList, "route_operator_rib_session_starts_total", map[string]string{"module": "route0"})
	require.NotNil(t, starts0)
	require.Equal(t, uint64(2), starts0.GetCounter())

	ends0 := findMetric(metricList, "route_operator_rib_session_ends_total", map[string]string{"module": "route0"})
	require.NotNil(t, ends0)
	require.Equal(t, uint64(1), ends0.GetCounter())

	starts1 := findMetric(metricList, "route_operator_rib_session_starts_total", map[string]string{"module": "route1"})
	require.NotNil(t, starts1)
	require.Equal(t, uint64(1), starts1.GetCounter())

	updates := findMetric(metricList, "route_operator_rib_feed_updates_total", map[string]string{})
	require.NotNil(t, updates)
	require.Equal(t, uint64(5), updates.GetCounter())
}

// TestMetrics_NeighbourEvents verifies that the neighbour health gauge and
// resync/sync counters track the monitor's callback sequence, and that
// neighbour metrics are absent when the monitor is disabled.
func TestMetrics_NeighbourEvents(t *testing.T) {
	t.Run("MonitorEnabled", func(t *testing.T) {
		m := NewMetrics(newRIBStore(zap.NewNop()), true)

		m.OnNeighbourSynced()
		m.OnNeighbourHealthy()
		m.OnNeighbourError(errors.New("degraded"))
		m.OnNeighbourResynced()
		m.OnNeighbourHealthy()

		metricList := m.Collect()

		healthy := findMetric(metricList, "route_operator_neighbour_monitor_healthy", map[string]string{})
		require.NotNil(t, healthy)
		require.Equal(t, 1.0, healthy.GetGauge())

		resyncs := findMetric(metricList, "route_operator_neighbour_resyncs_total", map[string]string{})
		require.NotNil(t, resyncs)
		require.Equal(t, uint64(1), resyncs.GetCounter())

		syncs := findMetric(metricList, "route_operator_neighbour_syncs_total", map[string]string{})
		require.NotNil(t, syncs)
		require.Equal(t, uint64(2), syncs.GetCounter())
	})

	t.Run("MonitorDisabled", func(t *testing.T) {
		m := NewMetrics(newRIBStore(zap.NewNop()), false)

		m.OnNeighbourSynced()
		m.OnNeighbourHealthy()

		metricList := m.Collect()

		require.Nil(t, findMetric(metricList, "route_operator_neighbour_monitor_healthy", map[string]string{}))
		require.Nil(t, findMetric(metricList, "route_operator_neighbour_syncs_total", map[string]string{}))
	})
}

// TestMetrics_PullRIBStats verifies that RIB gauges are pulled from the
// RIBStore's live snapshot at Collect time.
func TestMetrics_PullRIBStats(t *testing.T) {
	store := newRIBStore(zap.NewNop())
	ribRef := store.GetOrCreate("route0")
	require.NoError(t, ribRef.AddUnicastRoute(
		netip.MustParsePrefix("10.0.0.0/24"),
		netip.MustParseAddr("192.168.1.1"),
		rib.RouteSourceStatic,
	))

	m := NewMetrics(store, false)

	metricList := m.Collect()

	prefixes := findMetric(metricList, "route_operator_rib_prefixes", map[string]string{"module": "route0"})
	require.NotNil(t, prefixes)
	require.Equal(t, 1.0, prefixes.GetGauge())

	routes := findMetric(metricList, "route_operator_rib_routes", map[string]string{"module": "route0"})
	require.NotNil(t, routes)
	require.Equal(t, 1.0, routes.GetGauge())

	require.NotNil(t, findMetric(
		metricList,
		"route_operator_rib_last_change_timestamp_seconds",
		map[string]string{"module": "route0"},
	))
}

// TestGatewayMetrics_FIBBuilt verifies that OnFIBBuilt renders the five FIB
// gauges from FIBBuildStats, keyed by gateway and module.
func TestGatewayMetrics_FIBBuilt(t *testing.T) {
	m := NewMetrics(newRIBStore(zap.NewNop()), false)

	gateway := m.Gateway("gw0")
	gateway.OnFIBBuilt("route0", FIBBuildStats{
		TotalRoutes:       4,
		SkippedPrefixes:   1,
		NeighbourNotFound: 2,
		PrefixesAdded:     3,
		FilteredRoutes:    5,
	})

	metricList := m.Collect()
	labels := map[string]string{"gateway": "gw0", "module": "route0"}

	entries := findMetric(metricList, "route_operator_fib_entries", labels)
	require.NotNil(t, entries)
	require.Equal(t, 3.0, entries.GetGauge())

	routes := findMetric(metricList, "route_operator_fib_routes", labels)
	require.NotNil(t, routes)
	require.Equal(t, 4.0, routes.GetGauge())

	unresolved := findMetric(metricList, "route_operator_fib_unresolved_nexthops", labels)
	require.NotNil(t, unresolved)
	require.Equal(t, 2.0, unresolved.GetGauge())

	skipped := findMetric(metricList, "route_operator_fib_skipped_prefixes", labels)
	require.NotNil(t, skipped)
	require.Equal(t, 1.0, skipped.GetGauge())

	filtered := findMetric(metricList, "route_operator_fib_filtered_routes", labels)
	require.NotNil(t, filtered)
	require.Equal(t, 5.0, filtered.GetGauge())
}

// TestGatewayMetrics_ObserveApply verifies that ObserveApply updates the
// apply counters and renders a non-empty duration histogram.
func TestGatewayMetrics_ObserveApply(t *testing.T) {
	m := NewMetrics(newRIBStore(zap.NewNop()), false)
	gateway := m.Gateway("gw0")

	gateway.ObserveApply(10*time.Millisecond, nil)
	gateway.ObserveApply(20*time.Millisecond, errors.New("failed"))

	metricList := m.Collect()

	apply := findMetric(metricList, "route_operator_gateway_apply_total", map[string]string{"gateway": "gw0"})
	require.NotNil(t, apply)
	require.Equal(t, uint64(2), apply.GetCounter())

	applyErrors := findMetric(
		metricList,
		"route_operator_gateway_apply_errors_total",
		map[string]string{"gateway": "gw0"},
	)
	require.NotNil(t, applyErrors)
	require.Equal(t, uint64(1), applyErrors.GetCounter())

	duration := findMetric(
		metricList,
		"route_operator_gateway_apply_duration_seconds",
		map[string]string{"gateway": "gw0"},
	)
	require.NotNil(t, duration)
	require.Equal(t, uint64(2), duration.GetHistogram().GetTotalCount())
}

// TestMeteredActuator verifies that meteredActuator times and reports every
// Apply call, returns the inner error unchanged, and delegates Close.
func TestMeteredActuator(t *testing.T) {
	inner := &fakeActuator{applyErr: errors.New("gateway unreachable")}
	m := NewMetrics(newRIBStore(zap.NewNop()), false)
	gateway := m.Gateway("gw0")

	actuator := newMeteredActuator(inner, gateway)

	err := actuator.Apply(t.Context(), RouteSnapshot{})
	require.ErrorIs(t, err, inner.applyErr)
	require.Equal(t, 1, inner.applyCalls)

	metricList := m.Collect()
	apply := findMetric(metricList, "route_operator_gateway_apply_total", map[string]string{"gateway": "gw0"})
	require.NotNil(t, apply)
	require.Equal(t, uint64(1), apply.GetCounter())

	applyErrors := findMetric(
		metricList,
		"route_operator_gateway_apply_errors_total",
		map[string]string{"gateway": "gw0"},
	)
	require.NotNil(t, applyErrors)
	require.Equal(t, uint64(1), applyErrors.GetCounter())

	require.NoError(t, actuator.Close())
	require.Equal(t, 1, inner.closeCalls)
}
