package commonpb

import (
	"math"

	"github.com/yanet-platform/yanet2/common/go/metrics"
)

func FromMetricValue(v metrics.IsMetricValue) isMetric_Value {
	switch v := v.(type) {
	case *metrics.Counter:
		return &Metric_Counter{
			Counter: v.Load(),
		}
	case *metrics.Gauge:
		return &Metric_Gauge{
			Gauge: v.Load(),
		}
	case *metrics.Histogram:
		h := v
		boundsCount := len(h.Bounds)

		// one extra bucket for +inf bound
		buckets := make([]*Bucket, 0, boundsCount+1)

		for i := range boundsCount {
			buckets = append(buckets, &Bucket{
				Count:      h.Buckets[i].Load(),
				UpperBound: h.Bounds[i],
			})
		}

		buckets = append(buckets, &Bucket{Count: h.Buckets[boundsCount].Load(), UpperBound: math.Inf(1)})
		return &Metric_Histogram{
			Histogram: &Histogram{
				Buckets: buckets,
			},
		}
	}
	return nil
}
