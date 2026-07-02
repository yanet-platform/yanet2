package commonpb

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBucket_MarshalJSON verifies that a bucket with a finite upper bound
// serializes both fields in the order count, upper_bound.
func TestBucket_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		bucket   *Bucket
		expected string
	}{
		{
			name:     "finite upper bound",
			bucket:   &Bucket{Count: 7, UpperBound: 100},
			expected: `{"count":7,"upper_bound":100}`,
		},
		{
			name:     "infinite upper bound",
			bucket:   &Bucket{Count: 3, UpperBound: math.Inf(1)},
			expected: `{"count":3,"upper_bound":null}`,
		},
		{
			name:     "NaN upper bound",
			bucket:   &Bucket{Count: 9, UpperBound: math.NaN()},
			expected: `{"count":9,"upper_bound":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.bucket)
			require.NoError(t, err)
			require.Equal(t, tt.expected, string(data))
		})
	}
}

// TestMetric_MarshalJSON_HistogramWithInfBucket is the regression test for
// the gateway HTTP proxy path: encoding/json must traverse through the
// Metric_Histogram oneof wrapper into Bucket.MarshalJSON without error.
func TestMetric_MarshalJSON_HistogramWithInfBucket(t *testing.T) {
	metric := &Metric{
		Name: "latency",
		Value: &Metric_Histogram{
			Histogram: &Histogram{
				Buckets: []*Bucket{
					{Count: 5, UpperBound: 10},
					{Count: 2, UpperBound: math.Inf(1)},
				},
				TotalCount: 7,
			},
		},
	}

	data, err := json.Marshal(metric)
	require.NoError(t, err)
	require.Contains(t, string(data), `{"count":5,"upper_bound":10}`)
	require.Contains(t, string(data), `{"count":2,"upper_bound":null}`)
}
