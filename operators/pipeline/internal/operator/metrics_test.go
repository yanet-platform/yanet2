package operator_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/pipeline/internal/operator"
)

// Test_GatewayMetrics_Collect_CountsVxlanUpdates verifies that updates of
// the vxlan device kind are counted, and counted again as errors when they
// fail, under the device-vxlan label.
func Test_GatewayMetrics_Collect_CountsVxlanUpdates(t *testing.T) {
	metrics := operator.NewGatewayMetrics("gw0")

	metrics.OnResourceUpdated("device-vxlan", nil)
	metrics.OnResourceUpdated("device-vxlan", errors.New("update failed"))

	counters := map[string]uint64{}
	for _, metric := range metrics.Collect() {
		if hasLabel(metric, "kind", "device-vxlan") {
			counters[metric.GetName()] = metric.GetCounter()
		}
	}
	require.Equal(t, map[string]uint64{
		"pipeline_operator_resource_update_total":        2,
		"pipeline_operator_resource_update_errors_total": 1,
	}, counters)
}

// hasLabel reports whether the metric carries the label with the value.
func hasLabel(metric *commonpb.Metric, name, value string) bool {
	for _, label := range metric.GetLabels() {
		if label.GetName() == name && label.GetValue() == value {
			return true
		}
	}
	return false
}
