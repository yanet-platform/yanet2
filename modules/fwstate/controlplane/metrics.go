package fwstate

import (
	"context"
	"time"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	fwstatepb "github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// metricsSource provides the module's metrics, filtered by tags.
type metricsSource interface {
	Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error)
}

// MetricsService exposes FWState module metrics over its own gRPC service.
type MetricsService struct {
	fwstatepb.UnimplementedMetricsServiceServer

	source metricsSource
}

// NewMetricsService creates a MetricsService backed by source.
func NewMetricsService(source metricsSource) *MetricsService {
	return &MetricsService{source: source}
}

// GetMetrics returns a snapshot of FWState module metrics matching the
// request's tags.
func (m *MetricsService) GetMetrics(
	ctx context.Context,
	req *commonpb.GetMetricsRequest,
) (*commonpb.GetMetricsResponse, error) {
	all, err := m.source.Metrics(req.GetTags()...)
	if err != nil {
		return nil, err
	}

	return &commonpb.GetMetricsResponse{Metrics: all}, nil
}

// fwstateStructuralCounters lists the fixed fwstate counters whose metrics
// carry no "counter" label.
//
// It MUST mirror the named cases of the switch in emitCounterMetrics.
// Anything else is exported per-entry with a "counter" label. Used to
// derive the counter-name query so per-entry counters are not read from
// shm when filtered out.
var fwstateStructuralCounters = []string{
	"fwstate_sync", "fwstate_passthrough",
	"fwstate_sync_v4_inserted", "fwstate_sync_v6_inserted",
	"fwstate_sync_v4_insert_failed", "fwstate_sync_v6_insert_failed",
	"fwstate_sync_v4_suppressed", "fwstate_sync_v6_suppressed",
	"fwstate_external_dropped", "fwstate_internal_forwarded",
	"rx", "tx", "drop", "pending_input", "pending_output",
}

// Metrics returns FWState module metrics matching tags: per-config map
// statistics (gauge), dataplane counters, and gRPC call metrics.
//
// Gauge metrics are emitted per address family (af=ipv4|ipv6) for every
// loaded fwstate config.
//
// Labels:
//   - config:        fwstate config name (all metrics)
//   - af:            address family, "ipv4" or "ipv6" (map stats only)
//   - grpc_type:     always "unary" (gRPC metrics)
//   - grpc_service:  fully-qualified gRPC service name (gRPC metrics)
//   - grpc_method:   RPC name (gRPC metrics)
//   - grpc_code:     gRPC status code string (grpc_server_handled_total only)
func (m *FWStateService) Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	result := m.collectMapStats()

	dpMetrics, err := m.collectDataplaneMetrics(tags)
	if err != nil {
		return nil, err
	}
	result = append(result, dpMetrics...)
	if m.metrics != nil {
		result = append(result, m.metrics.Collect()...)
	}
	return metrics.Filter(result, tags), nil
}

// collectMapStats emits gauge metrics derived from the per-config map
// statistics (GetMapsStats) for both IPv4 and IPv6 address families.
func (m *FWStateService) collectMapStats() []*commonpb.Metric {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	now := time.Now()

	var result []*commonpb.Metric
	for name, config := range m.configs {
		mapsStats := config.GetMapsStats()

		result = append(result, collectMapStatsForAF(name, "ipv4", now, mapsStats.IPv4)...)
		result = append(result, collectMapStatsForAF(name, "ipv6", now, mapsStats.IPv6)...)
	}

	return result
}

// collectDataplaneMetrics emits per-config packet/byte counters read from the
// dataplane counter storage via DPConfig.
//
// A "counter" tag is pushed down into the dataplane counter read, so
// per-entry counters excluded by tags are never read from shared memory.
// Counter metrics are omitted when all worker values are zero to reduce
// output noise.
//
// Labels:
//   - config:   fwstate config name
//   - device:   dataplane device name
//   - pipeline: pipeline name
//   - function: pipeline function name
//   - chain:    pipeline chain name
//   - counter:  dataplane counter name (fwstate_counter_packets /
//     fwstate_counter_bytes only)
func (m *FWStateService) collectDataplaneMetrics(tags []*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	dpConfig := m.agent.DPConfig()
	if dpConfig == nil {
		return []*commonpb.Metric{}, nil
	}

	positions := dpConfig.AllModulePositions(moduleType)

	names, read := metrics.Query(tags,
		metrics.WithStructuralCounters(fwstateStructuralCounters),
		metrics.WithUnknownEntryCounters(),
	)

	result := make([]*commonpb.Metric, 0)
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

		for _, counter := range counters {
			result = append(result, emitCounterMetrics(counter, baseLabels)...)
		}
	}

	return result, nil
}

// emitCounterMetrics converts a single dataplane counter into metric series.
//
// The dataplane registers a set of well-known fwstate counters (see
// fwstate_module_config_new) plus the generic per-module counters registered
// by cp_module_init: rx, tx, drop, pending_input, pending_output (each a
// size-2 [packets, bytes] vector) and hist_0..hist_5 (latency histograms, see
// skipHistCounters). Each known counter is exported under a dedicated metric
// name with the correct semantic suffix (_packets, _bytes or _entries). The
// histogram counters are skipped entirely (proper histogram export is a
// separate task). Any other counter (one added in the future) is exported
// through a generic pair labelled with the counter name so it is never
// silently dropped.
//
// Counter series are omitted when all worker values are zero to reduce output
// noise.
func emitCounterMetrics(counter ffi.CounterInfo, baseLabels []*commonpb.Label) []*commonpb.Metric {
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
		return nil
	}

	switch counter.Name {
	case "fwstate_sync":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_sync_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_sync_bytes", bytes, baseLabels...),
		}
	case "fwstate_passthrough":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_passthrough_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_passthrough_bytes", bytes, baseLabels...),
		}
	// The *_inserted / *_insert_failed counters track state-table
	// entries (sync frames), not packets: a single sync packet
	// carries multiple frames and each frame bumps the counter once.
	// Export them with an _entries suffix so they are not rendered
	// under a packet/byte column.
	case "fwstate_sync_v4_inserted":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_sync_v4_inserted_entries", packets, baseLabels...),
		}
	case "fwstate_sync_v6_inserted":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_sync_v6_inserted_entries", packets, baseLabels...),
		}
	case "fwstate_sync_v4_insert_failed":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_sync_v4_insert_failed_entries", packets, baseLabels...),
		}
	case "fwstate_sync_v6_insert_failed":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_sync_v6_insert_failed_entries", packets, baseLabels...),
		}
	case "fwstate_sync_v4_suppressed":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_sync_v4_suppressed_entries", packets, baseLabels...),
		}
	case "fwstate_sync_v6_suppressed":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_sync_v6_suppressed_entries", packets, baseLabels...),
		}
	case "fwstate_external_dropped":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_external_dropped_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_external_dropped_bytes", bytes, baseLabels...),
		}
	case "fwstate_internal_forwarded":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_internal_forwarded_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_internal_forwarded_bytes", bytes, baseLabels...),
		}
	// Generic per-module counters registered by cp_module_init for every
	// module.
	case "rx":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_rx_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_rx_bytes", bytes, baseLabels...),
		}
	case "tx":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_tx_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_tx_bytes", bytes, baseLabels...),
		}
	// drop counts packets the module dropped itself. For fwstate this is
	// meaningful: external sync packets are dropped when they cannot be
	// inserted, so it deserves a dedicated pair like the others rather than
	// falling through to the generic arm.
	case "drop":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_drop_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_drop_bytes", bytes, baseLabels...),
		}
	case "pending_input":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_pending_input_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_pending_input_bytes", bytes, baseLabels...),
		}
	case "pending_output":
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_pending_output_packets", packets, baseLabels...),
			commonpb.NewMetricCounter("fwstate_pending_output_bytes", bytes, baseLabels...),
		}
	case "hist_0", "hist_1", "hist_2", "hist_3", "hist_4", "hist_5":
		// TODO: handle
		return nil
	default:
		// Any counter not listed above (e.g. one added in the future) is
		// exported through a generic pair labelled with the counter name so it
		// is never silently dropped.
		counterLabels := append(
			baseLabels,
			&commonpb.Label{Name: "counter", Value: counter.Name},
		)
		return []*commonpb.Metric{
			commonpb.NewMetricCounter("fwstate_counter_packets", packets, counterLabels...),
			commonpb.NewMetricCounter("fwstate_counter_bytes", bytes, counterLabels...),
		}
	}
}

// collectMapStatsForAF builds the gauge metric set for a single address family
// of a single fwstate config.
func collectMapStatsForAF(configName, af string, now time.Time, stats mapStats) []*commonpb.Metric {
	labels := []*commonpb.Label{
		{Name: "config", Value: configName},
		{Name: "af", Value: af},
	}

	// MaxDeadline is an absolute timestamp on the dataplane's monotonic clock.
	// Export the remaining time-to-live (clamped at zero once the deadline has
	// passed).
	deadlineTTL := uint64(0)
	if nowNS := uint64(now.UnixNano()); stats.MaxDeadline > nowNS {
		deadlineTTL = stats.MaxDeadline - nowNS
	}

	return []*commonpb.Metric{
		commonpb.NewMetricGauge("fwstate_index_size", float64(stats.IndexSize), labels...),
		commonpb.NewMetricGauge("fwstate_extra_bucket_count", float64(stats.ExtraBucketCount), labels...),
		commonpb.NewMetricGauge("fwstate_max_chain_length", float64(stats.MaxChainLength), labels...),
		commonpb.NewMetricGauge("fwstate_layer_count", float64(stats.LayerCount), labels...),
		commonpb.NewMetricGauge("fwstate_total_elements", float64(stats.TotalElements), labels...),
		commonpb.NewMetricGauge("fwstate_max_deadline_ns", float64(deadlineTTL), labels...),
		commonpb.NewMetricGauge("fwstate_memory_bytes", float64(stats.MemoryUsed), labels...),
	}
}

// retention snapshots the live fwstate config names and returns a predicate
// that keeps series whose "config" label is still live (or absent).
func (m *FWStateService) retention() func(metrics.MetricID) bool {
	configNames := m.configNamesSet()

	return func(id metrics.MetricID) bool {
		config := id.Labels["config"]
		if config == "" {
			return true
		}

		_, ok := configNames[config]
		return ok
	}
}

func (m *FWStateService) configNamesSet() map[string]struct{} {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	configNames := make(map[string]struct{}, len(m.configs))
	for name := range m.configs {
		configNames[name] = struct{}{}
	}
	return configNames
}
