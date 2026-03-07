package commonpb

import (
	"math"

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
		boundsCount := len(v.Bounds)

		// one extra bucket for +inf bound
		buckets := make([]*Bucket, 0, boundsCount+1)
		var totalCount uint64

		for i := range boundsCount {
			count := v.Buckets[i].Load()
			totalCount += count
			buckets = append(buckets, &Bucket{
				Count:      count,
				UpperBound: v.Bounds[i],
			})
		}

		infCount := v.Buckets[boundsCount].Load()
		totalCount += infCount
		buckets = append(buckets, &Bucket{Count: infCount, UpperBound: math.Inf(1)})
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
