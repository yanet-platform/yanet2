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

// portMetricsCollector provides collected metrics that describe the ports
// rather than the dataplane instance the gateway serves.
type portMetricsCollector interface {
	CollectPortMetrics() []*commonpb.Metric
}

// MetricsService exposes gateway gRPC server metrics and service-specific
// metrics over its own gRPC service.
type MetricsService struct {
	ynpb.UnimplementedMetricsServiceServer

	collectors []metricsCollector
}

// NewMetricsService creates a MetricsService backed by the given collectors.
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

// PortMetricsService serves hardware port counters over its own gRPC
// service.
//
// Every gateway returns the same values, so scraping any one of them
// records a port's counters once.
type PortMetricsService struct {
	ynpb.UnimplementedPortMetricsServiceServer

	collectors []portMetricsCollector
}

// NewPortMetricsService creates a service backed by the given collectors.
func NewPortMetricsService(collectors ...portMetricsCollector) *PortMetricsService {
	return &PortMetricsService{collectors: collectors}
}

// GetMetrics returns a snapshot of all port metrics.
func (m *PortMetricsService) GetMetrics(
	ctx context.Context,
	req *commonpb.GetMetricsRequest,
) (*commonpb.GetMetricsResponse, error) {
	all := make([]*commonpb.Metric, 0)
	for _, collector := range m.collectors {
		all = append(all, collector.CollectPortMetrics()...)
	}

	return &commonpb.GetMetricsResponse{Metrics: metrics.Filter(all, req.GetTags())}, nil
}

func metricsCollectors(server metricsCollector, entries []serviceEntry) []metricsCollector {
	collectors := []metricsCollector{server}
	for _, entry := range entries {
		if collector, ok := entry.Service.(metricsCollector); ok {
			collectors = append(collectors, collector)
		}
	}
	return collectors
}

func portMetricsCollectors(entries []serviceEntry) []portMetricsCollector {
	collectors := make([]portMetricsCollector, 0, len(entries))
	for _, entry := range entries {
		if collector, ok := entry.Service.(portMetricsCollector); ok {
			collectors = append(collectors, collector)
		}
	}
	return collectors
}
