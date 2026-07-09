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

// MetricsService exposes Forward module per-rule metrics over its own gRPC service.
type MetricsService struct {
	forwardpb.UnimplementedMetricsServiceServer

	source metricsSource
}

// NewMetricsService creates a MetricsService backed by source.
func NewMetricsService(source metricsSource) *MetricsService {
	return &MetricsService{source: source}
}

// GetMetrics returns a snapshot of Forward per-rule metrics.
func (m *MetricsService) GetMetrics(ctx context.Context, req *commonpb.GetMetricsRequest) (*commonpb.GetMetricsResponse, error) {
	all, err := m.source.Metrics()
	if err != nil {
		return nil, err
	}

	return &commonpb.GetMetricsResponse{Metrics: all}, nil
}

// makeCounter builds a counter metric with the provided name, value, and labels.
func makeCounter(name string, value uint64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Counter{Counter: value},
	}
}

// Metrics returns Forward per-rule metrics collected from the dataplane.
func (m *ForwardService) Metrics() ([]*commonpb.Metric, error) {
	return m.collectDataplaneMetrics()
}

// collectDataplaneMetrics gathers packet and byte counters for all configured Forward dataplane rules.
func (m *ForwardService) collectDataplaneMetrics() ([]*commonpb.Metric, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*commonpb.Metric, 0)
	for configName, config := range m.configs {
		counterNames := ruleCounterNames(config.rules)
		for _, counter := range m.backend.ModuleCounters(configName, counterNames) {
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
				{Name: "device", Value: counter.Device},
				{Name: "pipeline", Value: counter.Pipeline},
				{Name: "function", Value: counter.Function},
				{Name: "chain", Value: counter.Chain},
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

func ruleCounterNames(rules []*forwardpb.Rule) []string {
	seen := make(map[string]struct{}, len(rules))
	names := make([]string, 0, len(rules))
	for _, rule := range rules {
		name := rule.Action.Counter
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	return names
}
