package acl_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

// spyMetricsSource records the tags it receives and returns a fixed slice
// of metrics for MetricsService wiring tests.
type spyMetricsSource struct {
	metrics []*commonpb.Metric

	receivedTags []*commonpb.MetricTag
}

func (m *spyMetricsSource) Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	m.receivedTags = tags
	return m.metrics, nil
}

// TestMetricsServiceGetMetricsForwardsTags verifies that GetMetrics forwards
// the request's tags into the source unchanged and returns exactly what the
// source produces, since filtering now lives inside Metrics.
func TestMetricsServiceGetMetricsForwardsTags(t *testing.T) {
	metrics := []*commonpb.Metric{
		{
			Name:  "acl_action_allow_packets",
			Value: &commonpb.Metric_Counter{Counter: 42},
		},
	}

	tests := []struct {
		name string
		tags []*commonpb.MetricTag
	}{
		{
			name: "no tags",
			tags: nil,
		},
		{
			name: "counter tag",
			tags: []*commonpb.MetricTag{{Name: "counter", Value: ""}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &spyMetricsSource{metrics: metrics}
			service := acl.NewMetricsService(source)

			response, err := service.GetMetrics(t.Context(), &commonpb.GetMetricsRequest{Tags: test.tags})

			require.NoError(t, err)
			require.Equal(t, test.tags, source.receivedTags)
			require.Equal(t, metrics, response.GetMetrics())
		})
	}
}
