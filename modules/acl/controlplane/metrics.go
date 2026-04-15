package acl

import (
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

type handlersMetrics struct {
	callLatencies *metrics.MetricMap[*metrics.Histogram]
}

func newHandlersMetrics() handlersMetrics {
	return handlersMetrics{
		callLatencies: metrics.NewMetricMap[*metrics.Histogram](),
	}
}

func (m *handlersMetrics) collect() []*commonpb.Metric {
	return commonpb.MetricRefsToProto(m.callLatencies.Metrics())
}

var defaultLatencyBoundsMS = []float64{
	1,
	2,
	5,
	10,
	25,
	50,
	75,
	100,
	150,
	200,
	300,
	400,
	500,
	600,
	700,
	800,
	900,
	1000,
	1500,
	2000,
	3000,
	4000,
	5000,
}

type handlerMetricTracker struct {
	metricID  metrics.MetricID
	startTime time.Time
	metrics   *handlersMetrics
	latencies []float64
}

func newHandlerMetricTracker(
	handlerName string,
	handlerMetrics *handlersMetrics,
	latencies []float64,
	labels metrics.Labels,
) *handlerMetricTracker {
	if handlerMetrics == nil || latencies == nil {
		return nil
	}
	id := metrics.MetricID{
		Name:   handlerName,
		Labels: labels,
	}
	return &handlerMetricTracker{
		metricID:  id,
		startTime: time.Now(),
		metrics:   handlerMetrics,
		latencies: latencies,
	}
}

func (m *handlerMetricTracker) Fix() {
	if m == nil {
		return
	}
	duration := time.Since(m.startTime)
	m.metrics.callLatencies.GetOrCreate(m.metricID, func() *metrics.Histogram {
		return metrics.NewHistogram(m.latencies)
	}).Observe(float64(duration.Milliseconds()))
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

// Metrics collects all ACL module metrics including per-pipeline packet counters,
// per-rule counters, ACL compilation info, and handler call latencies
//
// All metric names are prefixed with "acl_". Counter metrics are omitted when
// all worker values are zero to reduce noise in the output
//
// Labels:
//   - config:   ACL config name (all counter metrics)
//   - device:   dataplane device name (all counter metrics)
//   - pipeline: pipeline name (all counter metrics)
//   - function: pipeline function name (all counter metrics)
//   - chain:    pipeline chain name (all counter metrics)
//   - counter:  ACL rule counter name (acl_rule_packets / acl_rule_bytes only)
//   - handler:  gRPC handler name (acl_handler_call_latency_ms only)
func (m *ACLService) Metrics() ([]*commonpb.Metric, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.collectMetrics()
}

func (m *ACLService) collectMetrics() ([]*commonpb.Metric, error) {
	dpConfig := m.agent.DPConfig()
	positions := dpConfig.AllModulePositions("acl")

	result := make([]*commonpb.Metric, 0)
	gaugesEmitted := make(map[string]struct{})
	for _, pos := range positions {
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
			// Sum values across all workers
			var packets, bytes uint64
			for _, workerVals := range counter.Values {
				if len(workerVals) > 0 {
					packets += workerVals[0]
				}
				if len(workerVals) > 1 {
					bytes += workerVals[1]
				}
			}

			// Skip counters with no traffic to reduce output noise
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
				ruleLabels := append(baseLabels, &commonpb.Label{Name: "counter", Value: counter.Name})
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
				result = append(result,
					makeGauge("acl_compilation_time_ns", float64(info.CompilationTimeNs), configLabels...),
					makeGauge("acl_filter_rule_count_vlan", float64(info.FilterRuleCountVlan), configLabels...),
					makeGauge("acl_filter_rule_count_ip4", float64(info.FilterRuleCountIp4), configLabels...),
					makeGauge("acl_filter_rule_count_ip4_port", float64(info.FilterRuleCountIp4Port), configLabels...),
					makeGauge("acl_filter_rule_count_ip6", float64(info.FilterRuleCountIp6), configLabels...),
					makeGauge("acl_filter_rule_count_ip6_port", float64(info.FilterRuleCountIp6Port), configLabels...),
					makeGauge("acl_memory_bytes", float64(m.memoryBytes), configLabels...),
				)
			}
		}
	}

	result = append(result, m.handlerMetrics.collect()...)

	return result, nil
}
