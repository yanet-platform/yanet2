package gateway

import (
	"context"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// metricsCollector provides the gateway's collected gRPC server metrics.
type metricsCollector interface {
	Collect() []*commonpb.Metric
}

// MetricsService exposes gateway gRPC server metrics over its own gRPC
// service.
type MetricsService struct {
	ynpb.UnimplementedMetricsServiceServer

	collector metricsCollector
}

// NewMetricsService creates a MetricsService backed by collector.
func NewMetricsService(collector metricsCollector) *MetricsService {
	return &MetricsService{collector: collector}
}

// GetMetrics returns a snapshot of all gateway gRPC server metrics.
func (m *MetricsService) GetMetrics(
	ctx context.Context,
	req *ynpb.GetMetricsRequest,
) (*ynpb.GetMetricsResponse, error) {
	return &ynpb.GetMetricsResponse{Metrics: m.collector.Collect()}, nil
}
