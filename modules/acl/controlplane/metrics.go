package acl

import (
	"context"
	"errors"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// metricsSource provides the module's metrics, filtered by tags.
type metricsSource interface {
	Metrics(ctx context.Context, tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error)
	RuleMetrics(ctx context.Context, req *aclpb.GetMetricsRulesRequest) ([]*commonpb.Metric, error)
}

type moduleCounterReader interface {
	ModuleCounters(dataplaneConfig *ffi.DPConfig, position ffi.ModuleReference, counterNames []string) []ffi.CounterInfo
}

type countersByTagsReader interface {
	CountersByTags(dataplaneConfig *ffi.DPConfig, tags []ffi.CounterTag, query []string) ([]ffi.CounterGroup, error)
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
	all, err := m.source.Metrics(ctx, req.GetTags()...)
	if err != nil {
		return nil, err
	}

	return &commonpb.GetMetricsResponse{Metrics: all}, nil
}

// GetMetricsRules returns the per-rule counter metrics GetMetrics leaves out.
func (m *MetricsService) GetMetricsRules(ctx context.Context, req *aclpb.GetMetricsRulesRequest) (*commonpb.GetMetricsResponse, error) {
	all, err := m.source.RuleMetrics(ctx, req)
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
// Concurrent scrapes are coalesced into one shared collection of the
// per-position packet-counter reads, and every concurrent caller
// receives the same values filtered by its own tags.
//
// Per-rule counters are served by RuleMetrics and GetRulesCounters, not
// here. Counter metrics are omitted when all worker values are zero to
// reduce output noise.
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
func (m *ACLService) Metrics(ctx context.Context, tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	collected, err := m.moduleMetricsFlight.Do(ctx, m.collectDataplaneMetrics)
	if err != nil {
		return nil, err
	}

	var grpcMetrics []*commonpb.Metric
	if m.metrics != nil {
		grpcMetrics = m.metrics.Collect()
	}
	all := slices.Concat(collected, grpcMetrics)
	return metrics.Filter(all, tags), nil
}

// RuleMetrics returns ACL per-rule counter metrics for the selected
// positions: one packets and one bytes counter for every rule counter.
//
// One merged shared-memory read serves every selector: concurrent
// scrapes are coalesced into it, and selection of each caller's
// positions happens on its shared result.
//
// These are the counters Metrics leaves out, read from the runtime-kind
// storages it never touches. An empty request field matches every value.
// One read serves every position, and each metric's position comes from
// the tags its counter group carries. A selector value the counter-tag
// fields cannot carry is rejected as an invalid argument. Counter
// metrics are omitted when all worker values are zero to reduce output
// noise.
//
// Labels:
//   - config:    ACL config name
//   - device:    dataplane device name
//   - pipeline:  pipeline name
//   - function:  pipeline function name
//   - chain:     pipeline chain name
//   - counter:   rule counter name
func (m *ACLService) RuleMetrics(ctx context.Context, req *aclpb.GetMetricsRulesRequest) ([]*commonpb.Metric, error) {
	dpConfig := m.backend.DPConfig()
	if dpConfig == nil {
		return []*commonpb.Metric{}, nil
	}

	if err := validateRuleSelectors(req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	groups, err := m.readRuleCounterGroups(ctx, dpConfig)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, status.Errorf(
			codes.Internal, "failed to read rule counters: %v", err,
		)
	}

	result := make([]*commonpb.Metric, 0)
	for _, group := range groups {
		location := groupLocation(group.Tags)
		if !locationSelected(location, req) {
			continue
		}

		base := []*commonpb.Label{
			{Name: "config", Value: location["module_name"]},
			{Name: "device", Value: location["device"]},
			{Name: "pipeline", Value: location["pipeline"]},
			{Name: "function", Value: location["function"]},
			{Name: "chain", Value: location["chain"]},
		}

		for _, counter := range group.Counters {
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

			labels := make([]*commonpb.Label, 0, len(base)+1)
			labels = append(labels, base...)
			labels = append(labels, &commonpb.Label{
				Name:  "counter",
				Value: counter.Name,
			})

			result = append(result,
				commonpb.NewMetricCounter("acl_rule_packets", packets, labels...),
				commonpb.NewMetricCounter("acl_rule_bytes", bytes, labels...),
			)
		}
	}

	return result, nil
}

// DrainMetricsReads blocks until every in-flight metrics collection has
// finished.
//
// A collection outlives the request handlers that joined it, so the
// shared memory it reads may only be released after this returns.
func (m *ACLService) DrainMetricsReads() {
	m.moduleMetricsFlight.Drain()
	m.ruleMetricsFlight.Drain()
}

// readRuleCounterGroups returns the per-rule counter groups both
// rule-counter RPCs work on.
//
// The merged read carries no request selectors: every caller filters
// the shared result on its own, so one shared-memory read of the family
// is in flight at a time and releasing that memory is safe after the
// drain.
func (m *ACLService) readRuleCounterGroups(ctx context.Context, dpConfig *ffi.DPConfig) ([]ffi.CounterGroup, error) {
	return m.ruleMetricsFlight.Do(ctx, func() ([]ffi.CounterGroup, error) {
		if reader, ok := m.backend.(countersByTagsReader); ok {
			return reader.CountersByTags(dpConfig, ruleCounterBaseTags(), nil)
		}
		return dpConfig.CountersByTags(ruleCounterBaseTags(), nil)
	})
}

// validateRuleSelectors rejects request selectors the fixed-size
// counter-tag fields cannot carry, before any shared-memory read.
func validateRuleSelectors(req *aclpb.GetMetricsRulesRequest) error {
	for _, selector := range requestSelectors(req) {
		key, value := selector[0], selector[1]
		if value == "" || value == "*" {
			continue
		}

		if err := ffi.ValidateTag(ffi.CounterTag{Key: key, Value: value}); err != nil {
			return err
		}
	}

	return nil
}

// ruleCounterBaseTags returns the fixed tag set every per-rule counter
// read matches on: the module's own counters of the runtime kind.
func ruleCounterBaseTags() []ffi.CounterTag {
	return []ffi.CounterTag{
		{Key: "module_type", Value: moduleType},
		{Key: "kind", Value: "runtime"},
	}
}

func locationSelected(location map[string]string, req *aclpb.GetMetricsRulesRequest) bool {
	for _, selector := range requestSelectors(req) {
		key, value := selector[0], selector[1]
		if value != "" && value != location[key] {
			return false
		}
	}

	return true
}

func requestSelectors(req *aclpb.GetMetricsRulesRequest) [][2]string {
	return [][2]string{
		{"module_name", req.GetConfig()},
		{"device", req.GetDevice()},
		{"pipeline", req.GetPipeline()},
		{"function", req.GetFunction()},
		{"chain", req.GetChain()},
	}
}

func groupLocation(tags []ffi.CounterTag) map[string]string {
	location := make(map[string]string, len(tags))
	for _, tag := range tags {
		location[tag.Key] = tag.Value
	}

	return location
}

func (m *ACLService) collectDataplaneMetrics() ([]*commonpb.Metric, error) {
	snapshot := m.metricsState.load()

	dpConfig := m.backend.DPConfig()
	if dpConfig == nil {
		return []*commonpb.Metric{}, nil
	}

	positions := dpConfig.AllModulePositions(moduleType)

	names := aclStructuralCounters

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
