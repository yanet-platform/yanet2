package forward

import (
	"context"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// metricsSource provides the module's collected metrics.
type metricsSource interface {
	Metrics() ([]*commonpb.Metric, error)
}

// MetricsService exposes Forward module metrics over its own gRPC service.
type MetricsService struct {
	forwardpb.UnimplementedMetricsServiceServer

	source metricsSource
}

// NewMetricsService creates a MetricsService backed by source.
func NewMetricsService(source metricsSource) *MetricsService {
	return &MetricsService{source: source}
}

// GetMetrics returns a snapshot of all Forward module metrics.
func (m *MetricsService) GetMetrics(ctx context.Context, req *forwardpb.GetMetricsRequest) (*forwardpb.GetMetricsResponse, error) {
	all, err := m.source.Metrics()
	if err != nil {
		return nil, err
	}

	return &forwardpb.GetMetricsResponse{Metrics: all}, nil
}

// makeCounter builds a counter metric with the provided name, value, and labels.
func makeCounter(name string, value uint64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Counter{Counter: value},
	}
}

// Metrics returns all Forward service metrics collected from the dataplane and local sources.
func (m *ForwardService) Metrics() ([]*commonpb.Metric, error) {
	metrics, err := m.collectDataplaneMetrics()
	if err != nil {
		return nil, err
	}
	if m.metrics != nil {
		metrics = append(metrics, m.metrics.Collect()...)
	}
	return metrics, nil
}

// collectDataplaneMetrics gathers packet and byte counters for all configured Forward dataplane rules.
func (m *ForwardService) collectDataplaneMetrics() ([]*commonpb.Metric, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*commonpb.Metric, 0)
	for configName := range m.configs {
		for _, counter := range m.backend.ModuleCounters(configName) {
			var packets, bytes uint64
			for _, instance := range counter.Values {
				if len(instance) > 0 {
					packets += instance[0]
				}
				if len(instance) > 1 {
					bytes += instance[1]
				}
			}

			labels := []*commonpb.Label{
				{Name: "config", Value: configName},
				{Name: "counter", Value: counter.Name},
			}

			result = append(result,
				makeCounter("forward_rule_packets", packets, labels...),
				makeCounter("forward_rule_bytes", bytes, labels...),
			)
		}
	}

	return result, nil
}
