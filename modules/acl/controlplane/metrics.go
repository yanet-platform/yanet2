package acl

import (
	"context"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// metricsSource provides the module's metrics, filtered by tags.
type metricsSource interface {
	Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error)
}

type moduleCounterReader interface {
	ModuleCounters(dataplaneConfig *ffi.DPConfig, position ffi.ModuleReference, counterNames []string) []ffi.CounterInfo
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

// GetMetrics returns a snapshot of ACL module metrics matching the
// request's tags.
func (m *MetricsService) GetMetrics(ctx context.Context, req *commonpb.GetMetricsRequest) (*commonpb.GetMetricsResponse, error) {
	all, err := m.source.Metrics(req.GetTags()...)
	if err != nil {
		return nil, err
	}

	return &commonpb.GetMetricsResponse{Metrics: all}, nil
}

// aclStructuralCounters lists the fixed ACL counters whose metrics carry no
// "counter" label.
//
// It MUST mirror the named cases of the switch in collectDataplaneMetrics.
// A per-rule counter is anything not in this set; the metrics read queries
// only these names, so per-rule counters are never read for metrics.
var aclStructuralCounters = []string{
	"acl_no_match", "acl_action_allow", "acl_action_deny", "acl_action_count",
	"acl_action_check_state", "acl_action_create_state", "acl_action_unknown",
	"acl_state_miss", "acl_sync_sent", "rx", "tx", "drop", "pending_input",
	"pending_output",
}

// Metrics returns ACL module metrics matching tags: per-pipeline packet
// counters, ACL compilation info, and gRPC call metrics.
//
// Per-rule counters are served by GetRulesCounters, not here. Counter
// metrics are omitted when all worker values are zero to reduce output
// noise.
//
// Labels:
//   - config:        ACL config name (all counter metrics)
//   - device:        dataplane device name (all counter metrics)
//   - pipeline:      pipeline name (all counter metrics)
//   - function:      pipeline function name (all counter metrics)
//   - chain:         pipeline chain name (all counter metrics)
//   - grpc_type:     always "unary" (gRPC metrics)
//   - grpc_service:  fully-qualified gRPC service name (gRPC metrics)
//   - grpc_method:   RPC name (gRPC metrics)
//   - grpc_code:     gRPC status code string (grpc_server_handled_total only)
func (m *ACLService) Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	all, err := m.collectDataplaneMetrics(tags)
	if err != nil {
		return nil, err
	}
	if m.metrics != nil {
		all = append(all, m.metrics.Collect()...)
	}
	return metrics.Filter(all, tags), nil
}

func (m *ACLService) collectDataplaneMetrics(tags []*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	snapshot := m.metricsState.load()

	dpConfig := m.backend.DPConfig()
	if dpConfig == nil {
		return []*commonpb.Metric{}, nil
	}

	positions := dpConfig.AllModulePositions(moduleType)

	names, read := metrics.Query(
		tags,
		metrics.WithStructuralCounters(aclStructuralCounters),
	)

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

		var counters []ffi.CounterInfo
		if read {
			if counterReader, ok := m.backend.(moduleCounterReader); ok {
				counters = counterReader.ModuleCounters(dpConfig, pos, names)
			} else {
				counters = dpConfig.ModuleCounters(
					pos.Device,
					pos.Pipeline,
					pos.Function,
					pos.Chain,
					moduleType,
					configName,
					names,
				)
			}
		}

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
					commonpb.NewMetricCounter("acl_no_match_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_no_match_bytes", bytes, baseLabels...),
				)
			case "acl_action_allow":
				result = append(result,
					commonpb.NewMetricCounter("acl_action_allow_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_action_allow_bytes", bytes, baseLabels...),
				)
			case "acl_action_deny":
				result = append(result,
					commonpb.NewMetricCounter("acl_action_deny_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_action_deny_bytes", bytes, baseLabels...),
				)
			case "acl_action_count":
				result = append(result,
					commonpb.NewMetricCounter("acl_action_count_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_action_count_bytes", bytes, baseLabels...),
				)
			case "acl_action_check_state":
				result = append(result,
					commonpb.NewMetricCounter("acl_action_check_state_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_action_check_state_bytes", bytes, baseLabels...),
				)
			case "acl_action_create_state":
				result = append(result,
					commonpb.NewMetricCounter("acl_action_create_state_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_action_create_state_bytes", bytes, baseLabels...),
				)
			case "acl_action_unknown":
				result = append(result,
					commonpb.NewMetricCounter("acl_action_unknown_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_action_unknown_bytes", bytes, baseLabels...),
				)
			case "acl_state_miss":
				result = append(result,
					commonpb.NewMetricCounter("acl_state_miss_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_state_miss_bytes", bytes, baseLabels...),
				)
			case "acl_sync_sent":
				result = append(result,
					commonpb.NewMetricCounter("acl_sync_sent_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_sync_sent_bytes", bytes, baseLabels...),
				)
			case "rx":
				result = append(result,
					commonpb.NewMetricCounter("acl_rx_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_rx_bytes", bytes, baseLabels...),
				)
			case "tx":
				result = append(result,
					commonpb.NewMetricCounter("acl_tx_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_tx_bytes", bytes, baseLabels...),
				)
			case "drop":
				result = append(result,
					commonpb.NewMetricCounter("acl_drop_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_drop_bytes", bytes, baseLabels...),
				)
			case "pending_input":
				result = append(result,
					commonpb.NewMetricCounter("acl_pending_input_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_pending_input_bytes", bytes, baseLabels...),
				)
			case "pending_output":
				result = append(result,
					commonpb.NewMetricCounter("acl_pending_output_packets", packets, baseLabels...),
					commonpb.NewMetricCounter("acl_pending_output_bytes", bytes, baseLabels...),
				)
			}
		}

		if _, ok := gaugesEmitted[configName]; !ok {
			gaugesEmitted[configName] = struct{}{}

			configLabels := []*commonpb.Label{
				{Name: "config", Value: configName},
			}

			if info, ok := snapshot.configInfo(configName); ok {
				result = append(
					result,
					commonpb.NewMetricGauge(
						"acl_compilation_time_ns",
						float64(info.CompilationTimeNs),
						configLabels...),
					commonpb.NewMetricGauge(
						"acl_filter_rule_count_vlan",
						float64(info.FilterRuleCountVlan),
						configLabels...),
					commonpb.NewMetricGauge(
						"acl_filter_rule_count_ip4",
						float64(info.FilterRuleCountIp4),
						configLabels...),
					commonpb.NewMetricGauge(
						"acl_filter_rule_count_ip4_port",
						float64(info.FilterRuleCountIp4Port),
						configLabels...),
					commonpb.NewMetricGauge(
						"acl_filter_rule_count_ip6",
						float64(info.FilterRuleCountIp6),
						configLabels...),
					commonpb.NewMetricGauge(
						"acl_filter_rule_count_ip6_port",
						float64(info.FilterRuleCountIp6Port),
						configLabels...),
				)
			}
		}
	}

	return result, nil
}
