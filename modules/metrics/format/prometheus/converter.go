package prometheus

import (
	"context"
	"strings"

	"github.com/yanet-platform/yanet2/common/commonpb"
	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
	"go.uber.org/zap"
)

type Converter struct {
	log    *zap.Logger
	result string
}

func NewConverter(log *zap.Logger) *Converter {
	if log == nil {
		log = zap.NewNop()
	}
	return &Converter{log: log}
}

func (m *Converter) Write(_ context.Context, metrics []metric.Metric) error {
	var b strings.Builder
	for _, mt := range metrics {
		if mt.Value == nil {
			m.log.Debug("skipping metric with nil value", zap.String("name", mt.Name))
			continue
		}
		mt.Write(&b)
	}
	m.result = b.String()
	return nil
}

func (m *Converter) Metrics() string {
	return m.result
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
		m.log.Debug("unrecognised metric value type", zap.String("name", metrics.GetName()))
		return &Undefine{}
	}
}
