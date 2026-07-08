package route

import (
	"context"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// metricsSource provides the module's collected metrics.
type metricsSource interface {
	Metrics() ([]*commonpb.Metric, error)
}

// MetricsService exposes route module metrics over its own gRPC service.
type MetricsService struct {
	routepb.UnimplementedMetricsServiceServer

	source metricsSource
}

// NewMetricsService creates a MetricsService backed by source.
func NewMetricsService(source metricsSource) *MetricsService {
	return &MetricsService{source: source}
}

// GetMetrics returns a snapshot of all route module metrics.
func (m *MetricsService) GetMetrics(ctx context.Context, req *commonpb.GetMetricsRequest) (*commonpb.GetMetricsResponse, error) {
	all, err := m.source.Metrics()
	if err != nil {
		return nil, err
	}

	return &commonpb.GetMetricsResponse{Metrics: all}, nil
}

func makeGauge(name string, value float64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Gauge{Gauge: value},
	}
}
