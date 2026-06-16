package acl

import (
	"context"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// metricsSource provides the module's collected metrics.
type metricsSource interface {
	Metrics() ([]*commonpb.Metric, error)
}

// MetricsService exposes ACL module metrics over its own gRPC service.
type MetricsService struct {
	aclpb.UnimplementedMetricsServiceServer

	source metricsSource
}

// NewMetricsService creates a MetricsService backed by source.
func NewMetricsService(source metricsSource) *MetricsService {
	return &MetricsService{source: source}
}

// GetMetrics returns a snapshot of all ACL module metrics.
func (m *MetricsService) GetMetrics(ctx context.Context, req *aclpb.GetMetricsRequest) (*aclpb.GetMetricsResponse, error) {
	all, err := m.source.Metrics()
	if err != nil {
		return nil, err
	}

	return &aclpb.GetMetricsResponse{Metrics: all}, nil
}

func makeGauge(name string, value float64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Gauge{Gauge: value},
	}
}

func makeCounter(name string, value uint64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Counter{Counter: value},
	}
}

// Metrics returns all ACL module metrics: per-pipeline packet counters,
// per-rule counters, ACL compilation info, and gRPC call metrics.
//
// Counter metrics are omitted when all worker values are zero to reduce output
// noise.
//
// Labels:
//   - config:        ACL config name (all counter metrics)
//   - device:        dataplane device name (all counter metrics)
//   - pipeline:      pipeline name (all counter metrics)
//   - function:      pipeline function name (all counter metrics)
//   - chain:         pipeline chain name (all counter metrics)
//   - counter:       ACL rule counter name (acl_rule_packets / acl_rule_bytes only)
//   - grpc_type:     always "unary" (gRPC metrics)
//   - grpc_service:  fully-qualified gRPC service name (gRPC metrics)
//   - grpc_method:   RPC name (gRPC metrics)
//   - grpc_code:     gRPC status code string (grpc_server_handled_total only)
func (m *ACLService) Metrics() ([]*commonpb.Metric, error) {
	metrics, err := m.collectDataplaneMetrics()
	if err != nil {
		return nil, err
	}
	if m.metrics != nil {
		metrics = append(metrics, m.metrics.Collect()...)
	}
	return metrics, nil
}

func (m *ACLService) collectDataplaneMetrics() ([]*commonpb.Metric, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dpConfig := m.backend.DPConfig()
	if dpConfig == nil {
		return []*commonpb.Metric{}, nil
	}

	positions := dpConfig.AllModulePositions("acl")

	result := make([]*commonpb.Metric, 0)
	gaugesEmitted := make(map[string]struct{})
	for pos := range positions {
		configName := pos.ModuleName

		baseLabels := []*commonpb.Label{
			{Name: "config", Value: configName},
			{Name: "device", Value: pos.Device},
			{Name: "pipeline", Value: pos.Pipeline},
			{Name: "function", Value: pos.Function},
			{Name: "chain", Value: pos.Chain},
		}

		counters := dpConfig.ModuleCounters(
			pos.Device,
			pos.Pipeline,
			pos.Function,
			pos.Chain,
			"acl",
			configName,
			nil,
		)

		for _, counter := range counters {
			var packets, bytes uint64
			for _, workerVals := range counter.Values {
				if len(workerVals) > 0 {
					packets += workerVals[0]
				}
				if len(workerVals) > 1 {
					bytes += workerVals[1]
				}
			}

			if packets == 0 && bytes == 0 {
				continue
			}

			switch counter.Name {
			case "acl_no_match":
				result = append(result,
					makeCounter("acl_no_match_packets", packets, baseLabels...),
					makeCounter("acl_no_match_bytes", bytes, baseLabels...),
				)
			case "acl_action_allow":
				result = append(result,
					makeCounter("acl_action_allow_packets", packets, baseLabels...),
					makeCounter("acl_action_allow_bytes", bytes, baseLabels...),
				)
			case "acl_action_deny":
				result = append(result,
					makeCounter("acl_action_deny_packets", packets, baseLabels...),
					makeCounter("acl_action_deny_bytes", bytes, baseLabels...),
				)
			case "acl_action_count":
				result = append(result,
					makeCounter("acl_action_count_packets", packets, baseLabels...),
					makeCounter("acl_action_count_bytes", bytes, baseLabels...),
				)
			case "acl_action_check_state":
				result = append(result,
					makeCounter("acl_action_check_state_packets", packets, baseLabels...),
					makeCounter("acl_action_check_state_bytes", bytes, baseLabels...),
				)
			case "acl_action_create_state":
				result = append(result,
					makeCounter("acl_action_create_state_packets", packets, baseLabels...),
					makeCounter("acl_action_create_state_bytes", bytes, baseLabels...),
				)
			case "acl_action_unknown":
				result = append(result,
					makeCounter("acl_action_unknown_packets", packets, baseLabels...),
					makeCounter("acl_action_unknown_bytes", bytes, baseLabels...),
				)
			case "acl_state_miss":
				result = append(result,
					makeCounter("acl_state_miss_packets", packets, baseLabels...),
					makeCounter("acl_state_miss_bytes", bytes, baseLabels...),
				)
			case "acl_sync_sent":
				result = append(result,
					makeCounter("acl_sync_sent_packets", packets, baseLabels...),
					makeCounter("acl_sync_sent_bytes", bytes, baseLabels...),
				)
			default:
				ruleLabels := append(
					baseLabels,
					&commonpb.Label{Name: "counter", Value: counter.Name},
				)
				result = append(result,
					makeCounter("acl_rule_packets", packets, ruleLabels...),
					makeCounter("acl_rule_bytes", bytes, ruleLabels...),
				)
			}
		}

		if _, ok := gaugesEmitted[configName]; !ok {
			gaugesEmitted[configName] = struct{}{}

			configLabels := []*commonpb.Label{
				{Name: "config", Value: configName},
			}

			if cfg, ok := m.configs[configName]; ok && cfg.acl != nil {
				info := cfg.acl.GetInfo()
				result = append(
					result,
					makeGauge(
						"acl_compilation_time_ns",
						float64(info.CompilationTimeNs),
						configLabels...),
					makeGauge(
						"acl_filter_rule_count_vlan",
						float64(info.FilterRuleCountVlan),
						configLabels...),
					makeGauge(
						"acl_filter_rule_count_ip4",
						float64(info.FilterRuleCountIp4),
						configLabels...),
					makeGauge(
						"acl_filter_rule_count_ip4_port",
						float64(info.FilterRuleCountIp4Port),
						configLabels...),
					makeGauge(
						"acl_filter_rule_count_ip6",
						float64(info.FilterRuleCountIp6),
						configLabels...),
					makeGauge(
						"acl_filter_rule_count_ip6_port",
						float64(info.FilterRuleCountIp6Port),
						configLabels...),
					makeGauge("acl_memory_bytes", float64(m.backend.MemoryBytes()), configLabels...),
				)
			}
		}
	}

	return result, nil
}
