package controller

import (
	"context"
	"fmt"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/modules/metrics/adapter"
	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
	"github.com/yanet-platform/yanet2/modules/metrics/format"
	"github.com/yanet-platform/yanet2/modules/metrics/metricspb"
	"go.uber.org/zap"
)

type Controller struct {
	formatter format.Formatter
	collector adapter.Collector
	log       *zap.Logger
}

func NewController(formatter format.Formatter, collector adapter.Collector, log *zap.Logger) *Controller {
	if log == nil {
		log = zap.NewNop()
	}
	return &Controller{
		formatter: formatter,
		collector: collector,
		log:       log,
	}
}

func (m *Controller) GetMetrics(ctx context.Context, _ *metricspb.GetMetricsRequest) (*metricspb.GetMetricsResponse, error) {
	raw, err := m.collector.Collect(ctx)
	if err != nil {
		m.log.Error("failed to collect metrics", zap.Error(err))
		return nil, fmt.Errorf("collect metrics: %w", err)
	}

	metrics := m.convertMetrics(raw)

	if err := m.formatter.Write(ctx, metrics); err != nil {
		m.log.Error("failed to format metrics", zap.Error(err))
		return nil, fmt.Errorf("format metrics: %w", err)
	}

	m.log.Debug("metrics collected",
		zap.Int("raw_count", len(raw)),
		zap.Int("converted_count", len(metrics)),
	)

	return &metricspb.GetMetricsResponse{
		Metrics: m.formatter.Metrics(),
	}, nil
}

func (m *Controller) convertMetrics(req []*commonpb.Metric) []metric.Metric {
	metrics := make([]metric.Metric, 0, len(req))

	for _, mtc := range req {
		name := mtc.GetName()

		labelsPB := mtc.GetLabels()
		labels := make([]metric.Label, 0, len(labelsPB))
		for _, l := range labelsPB {
			labels = append(labels, metric.Label{
				Name:  l.GetName(),
				Value: l.GetValue(),
			})
		}

		value := m.formatter.MetricValue(mtc)

		metrics = append(metrics, metric.Metric{
			Name:   name,
			Labels: labels,
			Value:  value,
		})
	}

	return metrics
}
