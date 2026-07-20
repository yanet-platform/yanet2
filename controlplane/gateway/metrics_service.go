package gateway

import (
	"context"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// metricsCollector provides collected metrics.
type metricsCollector interface {
	Collect() []*commonpb.Metric
}

// MetricsService exposes gateway gRPC server metrics and service-specific
// metrics over its own gRPC service.
type MetricsService struct {
	ynpb.UnimplementedMetricsServiceServer

	collectors []metricsCollector
}

// NewMetricsService creates a MetricsService backed by collector.
func NewMetricsService(collectors ...metricsCollector) *MetricsService {
	return &MetricsService{collectors: collectors}
}

// GetMetrics returns a snapshot of all gateway gRPC server metrics plus
// service-specific metrics.
func (m *MetricsService) GetMetrics(
	ctx context.Context,
	req *commonpb.GetMetricsRequest,
) (*commonpb.GetMetricsResponse, error) {
	all := make([]*commonpb.Metric, 0)
	for _, collector := range m.collectors {
		all = append(all, collector.Collect()...)
	}

	return &commonpb.GetMetricsResponse{Metrics: metrics.Filter(all, req.GetTags())}, nil
}

func metricsCollectors(server metricsCollector, entries []serviceEntry) []metricsCollector {
	collectors := []metricsCollector{server}
	for _, entry := range entries {
		if collector, ok := entry.service.(metricsCollector); ok {
			collectors = append(collectors, collector)
		}
	}
	return collectors
}
