package controller

import (
	"context"

	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
	"github.com/yanet-platform/yanet2/modules/metrics/format"
	"github.com/yanet-platform/yanet2/modules/metrics/metricspb"
)

type Controller struct {
	Formatter format.Formatter
}

func NewContoller(formatter format.Formatter) *Controller {
	return &Controller{
		Formatter: formatter,
	}
}

func (m *Controller) GetMetrics(ctx context.Context, req *metricspb.GetMetricsRequest) (*metricspb.GetMetricsResponse, error) {
	//TODO: validate and errors and logs

	metrics := m.convertMetrics(req)
	m.Formatter.Write(ctx, metrics)

	metricFormatted := m.Formatter.Metrics()

	return &metricspb.GetMetricsResponse{
		Metrics: metricFormatted,
	}, nil
}

func (m *Controller) convertMetrics(req *metricspb.GetMetricsRequest) []metric.Metric {
	metrics := make([]metric.Metric, 0, len(req.Metrics))

	for _, mtc := range req.Metrics {
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
