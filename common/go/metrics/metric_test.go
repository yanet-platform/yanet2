package metrics

import "testing"

func TestMetricIDEqualOrdered(t *testing.T) {
	tests := []struct {
		name string
		a, b MetricID
		want bool
	}{
		{
			name: "Equal",
			a:    MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "1"}}},
			b:    MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "1"}}},
			want: true,
		},
		{
			name: "DifferentName",
			a:    MetricID{Name: "test1"},
			b:    MetricID{Name: "test2"},
			want: false,
		},
		{
			name: "DifferentLabelValue",
			a:    MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "1"}}},
			b:    MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "2"}}},
			want: false,
		},
		{
			name: "DifferentLabelOrder",
			a:    MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}},
			b:    MetricID{Name: "test", Labels: []Label{{Name: "b", Value: "2"}, {Name: "a", Value: "1"}}},
			want: false,
		},
		{
			name: "DifferentLabelCount",
			a:    MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "1"}}},
			b:    MetricID{Name: "test", Labels: []Label{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}},
			want: false,
		},
		{
			name: "BothEmpty",
			a:    MetricID{Name: "test"},
			b:    MetricID{Name: "test"},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.EqualOrdered(tc.b); got != tc.want {
				t.Errorf("EqualOrdered() = %v, want %v", got, tc.want)
			}
		})
	}
}
