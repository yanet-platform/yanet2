package controller

import (
	"context"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/modules/metrics/adapter"
	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
	"github.com/yanet-platform/yanet2/modules/metrics/format"
	"github.com/yanet-platform/yanet2/modules/metrics/metricspb"
)

type Controller struct {
	Formatter format.Formatter
	Collector adapter.Collector
}

func NewContoller(formatter format.Formatter, collector adapter.Collector) *Controller {
	return &Controller{
		Formatter: formatter,
		Collector: collector,
	}
}

func (m *Controller) GetMetrics(ctx context.Context, req *metricspb.GetMetricsRequest) (*metricspb.GetMetricsResponse, error) {
	//TODO: validate and errors and logs

	messyMetrics, err := m.Collector.Collect(ctx)
	if err != nil {
		// TODO: error handling
	}

	metrics := m.convertMetrics(messyMetrics)
	m.Formatter.Write(ctx, metrics)

	metricFormatted := m.Formatter.Metrics()

	return &metricspb.GetMetricsResponse{
		Metrics: metricFormatted,
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

		value := m.Formatter.MetricValue(mtc)

		metrics = append(metrics, metric.Metric{
			Name:   name,
			Labels: labels,
			Value:  value,
		})
	}

	return metrics
}
