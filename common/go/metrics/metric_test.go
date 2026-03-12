package metrics

import "testing"

func TestMetricIDEquals(t *testing.T) {
	tests := []struct {
		name string
		a, b MetricID
		want bool
	}{
		{
			name: "Equal",
			a:    MetricID{Name: "test", Labels: Labels{"a": "1"}},
			b:    MetricID{Name: "test", Labels: Labels{"a": "1"}},
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
			a:    MetricID{Name: "test", Labels: Labels{"a": "1"}},
			b:    MetricID{Name: "test", Labels: Labels{"a": "2"}},
			want: false,
		},
		{
			name: "DifferentLabelKey",
			a:    MetricID{Name: "test", Labels: Labels{"a": "1"}},
			b:    MetricID{Name: "test", Labels: Labels{"b": "1"}},
			want: false,
		},
		{
			name: "DifferentLabelCount",
			a:    MetricID{Name: "test", Labels: Labels{"a": "1"}},
			b:    MetricID{Name: "test", Labels: Labels{"a": "1", "b": "2"}},
			want: false,
		},
		{
			name: "BothEmpty",
			a:    MetricID{Name: "test"},
			b:    MetricID{Name: "test"},
			want: true,
		},
		{
			name: "NilAndEmptyLabelsEqual",
			a:    MetricID{Name: "test", Labels: nil},
			b:    MetricID{Name: "test", Labels: Labels{}},
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
