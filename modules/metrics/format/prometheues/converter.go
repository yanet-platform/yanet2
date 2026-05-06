package prometheues

import (
	"context"
	"io"

	"github.com/yanet-platform/yanet2/common/commonpb"
	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
)

type FormatBuilder interface {
	io.Writer
	String() string
}

type Converter struct {
	Writer FormatBuilder
}

func NewConverter(w FormatBuilder) *Converter {
	return &Converter{Writer: w}
}

func (m *Converter) Write(ctx context.Context, metrics []metric.Metric) error {
	for _, metric := range metrics {
		metric.Write(m.Writer)
	}
	return nil
}

func (m *Converter) Metrics() string {
	return m.Writer.String()
}

func (m *Converter) MetricValue(metrics *commonpb.Metric) metric.Value {
	switch metrics.GetValue().(type) {
	case *commonpb.Metric_Counter:
		return Counter(metrics.GetCounter())
	case *commonpb.Metric_Gauge:
		return Gauge(metrics.GetGauge())
	case *commonpb.Metric_Histogram:
		h := metrics.GetHistogram()
		buckets := make([]Bucket, len(h.Buckets))
		for i, b := range h.Buckets {
			buckets[i] = Bucket{
				UpperBound: b.UpperBound,
				Count:      b.Count,
			}
		}
		return &Histogram{
			Buckets:    buckets,
			TotalCount: h.TotalCount,
		}
	default:
		return &Undefine{}
	}
}
