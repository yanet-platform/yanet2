package route

import (
	"context"
	"maps"
	"slices"

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

// makeCounter builds a counter metric with the provided name, value, and labels.
func makeCounter(name string, value uint64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Counter{Counter: value},
	}
}

// counterMapping projects one route dataplane counter onto the metric family
// that carries it.
type counterMapping struct {
	// Metric is the emitted metric family, without the _packets or
	// _bytes suffix.
	Metric string
	// Reason names the drop cause behind the counter.
	//
	// Empty for families that carry no reason label.
	Reason string
	// Family is the address family the counter observes.
	Family string
}

// counterMappings is the control plane's half of the counter contract with
// the route dataplane.
//
// Every counter the dataplane registers is spelled out here exactly once,
// so the table doubles as the documentation of what each series means. A
// counter missing from the table is never requested and never exported.
var counterMappings = map[string]counterMapping{
	"route_forwarded_v4": {Metric: "route_forwarded", Family: "v4"},
	"route_forwarded_v6": {Metric: "route_forwarded", Family: "v6"},

	"route_drop_no_route_v4": {Metric: "route_drop", Reason: "no_route", Family: "v4"},
	"route_drop_no_route_v6": {Metric: "route_drop", Reason: "no_route", Family: "v6"},

	"route_drop_ttl_expired_v4": {Metric: "route_drop", Reason: "ttl_expired", Family: "v4"},
	"route_drop_ttl_expired_v6": {Metric: "route_drop", Reason: "ttl_expired", Family: "v6"},

	// The ethertype is not IP, so the drop has no address family to
	// report.
	"route_drop_non_ip": {Metric: "route_drop", Reason: "non_ip", Family: "unknown"},

	"route_drop_empty_route_list_v4": {Metric: "route_drop", Reason: "empty_route_list", Family: "v4"},
	"route_drop_empty_route_list_v6": {Metric: "route_drop", Reason: "empty_route_list", Family: "v6"},

	"route_drop_device_unresolved_v4": {Metric: "route_drop", Reason: "device_unresolved", Family: "v4"},
	"route_drop_device_unresolved_v6": {Metric: "route_drop", Reason: "device_unresolved", Family: "v6"},
}

// counterNames is the counter query sent to the dataplane, sorted so the
// request is stable across scrapes.
var counterNames = slices.Sorted(maps.Keys(counterMappings))

// collectDataplaneMetrics gathers the module-level packet and byte counters
// of every config, summed across worker instances.
//
// Every known counter is emitted even when it reads zero. A series that
// disappears and reappears breaks rate() in Prometheus, and the empty route
// list counters are invariant canaries expected to read zero forever, which
// only works if they are visible.
func (m *RouteService) collectDataplaneMetrics() []*commonpb.Metric {
	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	result := make([]*commonpb.Metric, 0)
	for configName := range m.configs {
		for _, counter := range m.backend.ModuleCounters(configName, counterNames) {
			mapping, ok := counterMappings[counter.Name]
			if !ok {
				continue
			}

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
				{Name: "family", Value: mapping.Family},
			}
			if mapping.Reason != "" {
				labels = append(labels, &commonpb.Label{Name: "reason", Value: mapping.Reason})
			}

			result = append(result,
				makeCounter(mapping.Metric+"_packets", packets, labels...),
				makeCounter(mapping.Metric+"_bytes", bytes, labels...),
			)
		}
	}

	return result
}
