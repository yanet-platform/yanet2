package operator_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

// findMetric returns the metric named name whose label set equals labels,
// or nil when none does.
func findMetric(metricList []*commonpb.Metric, name string, labels map[string]string) *commonpb.Metric {
	for _, metric := range metricList {
		if metric.GetName() == name && len(metric.GetLabels()) == len(labels) {
			matched := true
			for _, label := range metric.GetLabels() {
				if value, ok := labels[label.GetName()]; !ok || value != label.GetValue() {
					matched = false
					break
				}
			}
			if matched {
				return metric
			}
		}
	}

	return nil
}

// Test_ReconcilerMetrics_Collect_RendersEvents verifies that the reconcile
// observer events update the counters and gauges the families render.
func Test_ReconcilerMetrics_Collect_RendersEvents(t *testing.T) {
	reconciler := operator.NewReconcilerMetrics("test_operator")

	reconciler.OnReconcileCompleted(nil)
	reconciler.OnReconcileCompleted(errors.New("boom"))
	reconciler.OnBackoffScheduled(2 * time.Second)
	reconciler.OnStateChanged(operator.ReconcilerStateApplying)

	metricList := reconciler.Collect()

	total := findMetric(metricList, "test_operator_reconcile_total", map[string]string{})
	require.NotNil(t, total)
	require.Equal(t, uint64(2), total.GetCounter())

	failures := findMetric(metricList, "test_operator_reconcile_errors_total", map[string]string{})
	require.NotNil(t, failures)
	require.Equal(t, uint64(1), failures.GetCounter())

	backoff := findMetric(metricList, "test_operator_backoff_seconds", map[string]string{})
	require.NotNil(t, backoff)
	require.Equal(t, 2.0, backoff.GetGauge())

	applying := findMetric(metricList, "test_operator_state", map[string]string{"state": "applying"})
	require.NotNil(t, applying)
	require.Equal(t, 1.0, applying.GetGauge())

	idle := findMetric(metricList, "test_operator_state", map[string]string{"state": "idle"})
	require.NotNil(t, idle)
	require.Equal(t, 0.0, idle.GetGauge())

	reconciler.OnBackoffReset()
	backoff = findMetric(reconciler.Collect(), "test_operator_backoff_seconds", map[string]string{})
	require.NotNil(t, backoff)
	require.Equal(t, 0.0, backoff.GetGauge())
}
