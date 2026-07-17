package metrics_test

import (
	"testing"

	"github.com/yanet-platform/yanet2/common/go/metrics"
)

func TestMetricIDEquals(t *testing.T) {
	tests := []struct {
		name string
		a, b metrics.MetricID
		want bool
	}{
		{
			name: "Equal",
			a:    metrics.MetricID{Name: "test", Labels: metrics.Labels{"a": "1"}},
			b:    metrics.MetricID{Name: "test", Labels: metrics.Labels{"a": "1"}},
			want: true,
		},
		{
			name: "DifferentName",
			a:    metrics.MetricID{Name: "test1"},
			b:    metrics.MetricID{Name: "test2"},
			want: false,
		},
		{
			name: "DifferentLabelValue",
			a:    metrics.MetricID{Name: "test", Labels: metrics.Labels{"a": "1"}},
			b:    metrics.MetricID{Name: "test", Labels: metrics.Labels{"a": "2"}},
			want: false,
		},
		{
			name: "DifferentLabelKey",
			a:    metrics.MetricID{Name: "test", Labels: metrics.Labels{"a": "1"}},
			b:    metrics.MetricID{Name: "test", Labels: metrics.Labels{"b": "1"}},
			want: false,
		},
		{
			name: "DifferentLabelCount",
			a:    metrics.MetricID{Name: "test", Labels: metrics.Labels{"a": "1"}},
			b:    metrics.MetricID{Name: "test", Labels: metrics.Labels{"a": "1", "b": "2"}},
			want: false,
		},
		{
			name: "BothEmpty",
			a:    metrics.MetricID{Name: "test"},
			b:    metrics.MetricID{Name: "test"},
			want: true,
		},
		{
			name: "NilAndEmptyLabelsEqual",
			a:    metrics.MetricID{Name: "test", Labels: nil},
			b:    metrics.MetricID{Name: "test", Labels: metrics.Labels{}},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Equals(tc.b); got != tc.want {
				t.Errorf("Equals() = %v, want %v", got, tc.want)
			}
		})
	}
}
