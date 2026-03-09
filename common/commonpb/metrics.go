package commonpb

import (
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

func MetricValueToProto(v metrics.IsMetricValue) isMetric_Value {
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
		bucketSnapshots := v.Snapshot()

		// NOTE: The buckets are populated with raw per-bucket counts, not cumulative counts.
		// This is a deliberate divergence from Prometheus/OpenTelemetry semantics where each
		// bucket count is cumulative (includes all observations ≤ upper bound).
		// Here, each bucket contains only the count of observations that fall within its
		// specific range (previous_bound < value ≤ upper_bound).
		buckets := make([]*Bucket, len(bucketSnapshots))
		var totalCount uint64

		for i := range bucketSnapshots {
			bucket := &bucketSnapshots[i]
			totalCount += bucket.Count
			buckets[i] = &Bucket{
				Count:      bucket.Count,
				UpperBound: bucket.UpperBound,
			}
		}

		return &Metric_Histogram{
			Histogram: &Histogram{
				Buckets:    buckets,
				TotalCount: totalCount,
			},
		}
	}

	return nil
}

func MetricLabelsToProto(labels []metrics.Label) []*Label {
	res := make([]*Label, 0, len(labels))
	for _, l := range labels {
		res = append(res, &Label{
			Name:  l.Name,
			Value: l.Value,
		})
	}
	return res
}

func MetricRefsToProto[T metrics.IsMetricValue](refs []metrics.Metric[T]) []*Metric {
	res := make([]*Metric, 0, len(refs))
	for _, ref := range refs {
		res = append(res, &Metric{
			Name:   ref.ID.Name,
			Labels: MetricLabelsToProto(ref.ID.Labels),
			Value:  MetricValueToProto(ref.Value),
		})
	}
	return res
}
