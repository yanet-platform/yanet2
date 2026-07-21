package forward

import (
	"context"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// metricsSource provides the module's metrics, filtered by tags.
type metricsSource interface {
	Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error)
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

// GetMetrics returns a snapshot of Forward per-rule metrics matching the
// request's tags.
func (m *MetricsService) GetMetrics(ctx context.Context, req *commonpb.GetMetricsRequest) (*commonpb.GetMetricsResponse, error) {
	all, err := m.source.Metrics(req.GetTags()...)
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

// Metrics returns Forward per-rule metrics matching tags, collected from the
// dataplane.
func (m *ForwardService) Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	all, err := m.collectDataplaneMetrics(tags)
	if err != nil {
		return nil, err
	}
	return metrics.Filter(all, tags), nil
}

// collectDataplaneMetrics gathers packet and byte counters for all configured
// Forward dataplane rules.
//
// A "counter" tag is pushed down into the dataplane counter read, so
// per-rule counters excluded by tags are never read from shared memory.
// Forward has no structural (non-per-rule) counters, so its full name list
// is exactly the rule set. An empty derived name list therefore means there
// is nothing to read, never "read everything", so the read is skipped
// rather than passed on to ModuleCounters, which treats an empty list as
// "all counters".
func (m *ForwardService) collectDataplaneMetrics(tags []*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*commonpb.Metric, 0)
	for configName, config := range m.configs {
		names, read := metrics.Query(tags, ruleCounterNames(config.Rules), nil)
		if !read || len(names) == 0 {
			continue
		}

		for _, counter := range m.backend.ModuleCounters(configName, names) {
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
