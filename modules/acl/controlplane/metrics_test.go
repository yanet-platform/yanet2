package acl_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// spyMetricsSource records the tags it receives and returns a fixed slice
// of metrics for MetricsService wiring tests.
type spyMetricsSource struct {
	metrics     []*commonpb.Metric
	ruleMetrics []*commonpb.Metric

	receivedTags []*commonpb.MetricTag
	receivedRule *aclpb.GetMetricsRulesRequest
}

type counterRead struct {
	dataplaneConfig *ffi.DPConfig
	counterNames    []string
}

type counterSpyBackend struct {
	*fakeBackend

	counterReads []counterRead
	counters     map[string][]ffi.CounterInfo
}

// newCounterSpyBackend returns a backend that records bounded counter reads.
func newCounterSpyBackend(
	dataplaneConfig *ffi.DPConfig,
	counters map[string][]ffi.CounterInfo,
) *counterSpyBackend {
	backend := newFakeBackend()
	backend.dpConfig = dataplaneConfig
	return &counterSpyBackend{fakeBackend: backend, counters: counters}
}

// ModuleCounters records the requested names before returning fixed values.
func (m *counterSpyBackend) ModuleCounters(
	dataplaneConfig *ffi.DPConfig,
	position ffi.ModuleReference,
	counterNames []string,
) []ffi.CounterInfo {
	m.counterReads = append(m.counterReads, counterRead{
		dataplaneConfig: dataplaneConfig,
		counterNames:    append([]string(nil), counterNames...),
	})
	return append([]ffi.CounterInfo(nil), m.counters[position.ModuleName]...)
}

// CounterReads returns the counter reads observed by the backend.
func (m *counterSpyBackend) CounterReads() []counterRead {
	return append([]counterRead(nil), m.counterReads...)
}

func (m *spyMetricsSource) Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	m.receivedTags = tags
	return m.metrics, nil
}

func (m *spyMetricsSource) RuleMetrics(req *aclpb.GetMetricsRulesRequest) ([]*commonpb.Metric, error) {
	m.receivedRule = req
	return m.ruleMetrics, nil
}

// verifies that an untagged scrape reads only the fixed counter set and
// exports generic worker totals without histogram or rule metrics.
func Test_ACLMetrics_UntaggedScrapeUsesBoundedGenericCounters(t *testing.T) {
	_, agent := newMetricsSnapshotHarness(t)
	dataplaneConfig := agent.DPConfig()
	backend := newCounterSpyBackend(dataplaneConfig, map[string][]ffi.CounterInfo{
		"acl0": {
			{Name: "rx", Values: [][]uint64{{1, 100}, {2, 200}}},
			{Name: "tx", Values: [][]uint64{{3, 300}, {4, 400}}},
			{Name: "drop", Values: [][]uint64{{5, 500}, {6, 600}}},
			{Name: "pending_input", Values: [][]uint64{{7, 700}, {8, 800}}},
			{Name: "pending_output", Values: [][]uint64{{9, 900}, {10, 1000}}},
			{Name: "hist_0", Values: [][]uint64{{11, 1100, 11000}}},
			{Name: "rule_counter", Values: [][]uint64{{12, 1200}}},
		},
	})
	service := acl.NewACLService(backend)

	collected, err := service.Metrics()
	require.NoError(t, err)

	expectedQuery := []string{
		"acl_no_match", "acl_action_allow", "acl_action_deny", "acl_action_count",
		"acl_action_check_state", "acl_action_create_state", "acl_action_unknown",
		"acl_state_miss", "acl_sync_sent", "rx", "tx", "drop", "pending_input",
		"pending_output",
	}
	reads := backend.CounterReads()
	require.Len(t, reads, 5)
	for _, read := range reads {
		require.Same(t, dataplaneConfig, read.dataplaneConfig)
		require.Equal(t, expectedQuery, read.counterNames)
	}

	expectedMetrics := map[string]uint64{
		"acl_rx_packets":             3,
		"acl_rx_bytes":               300,
		"acl_tx_packets":             7,
		"acl_tx_bytes":               700,
		"acl_drop_packets":           11,
		"acl_drop_bytes":             1100,
		"acl_pending_input_packets":  15,
		"acl_pending_input_bytes":    1500,
		"acl_pending_output_packets": 19,
		"acl_pending_output_bytes":   1900,
	}
	actualMetrics := map[string]uint64{}
	for _, metric := range collected {
		actualMetrics[metric.GetName()] = metric.GetCounter()
	}
	require.Len(t, collected, len(expectedMetrics))
	require.Equal(t, expectedMetrics, actualMetrics)
}

// TestMetricsServiceGetMetricsForwardsTags verifies that GetMetrics forwards
// the request's tags into the source unchanged and returns exactly what the
// source produces, since filtering now lives inside Metrics.
func TestMetricsServiceGetMetricsForwardsTags(t *testing.T) {
	metrics := []*commonpb.Metric{
		{
			Name:  "acl_action_allow_packets",
			Value: &commonpb.Metric_Counter{Counter: 42},
		},
	}

	tests := []struct {
		name string
		tags []*commonpb.MetricTag
	}{
		{
			name: "no tags",
			tags: nil,
		},
		{
			name: "counter tag",
			tags: []*commonpb.MetricTag{{Name: "counter", Value: ""}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &spyMetricsSource{metrics: metrics}
			service := acl.NewMetricsService(source)

			response, err := service.GetMetrics(t.Context(), &commonpb.GetMetricsRequest{Tags: test.tags})

			require.NoError(t, err)
			require.Equal(t, test.tags, source.receivedTags)
			require.Equal(t, metrics, response.GetMetrics())
		})
	}
}

// TestMetricsServiceGetMetricsRulesUsesRuleSource verifies that
// GetMetricsRules forwards its selectors to the rule source and returns what
// that source produces, keeping the two reads apart.
func TestMetricsServiceGetMetricsRulesUsesRuleSource(t *testing.T) {
	structural := []*commonpb.Metric{
		{
			Name:  "acl_action_allow_packets",
			Value: &commonpb.Metric_Counter{Counter: 42},
		},
	}
	rules := []*commonpb.Metric{
		{
			Name:  "acl_rule_packets",
			Value: &commonpb.Metric_Counter{Counter: 7},
		},
	}

	request := &aclpb.GetMetricsRulesRequest{Config: "test", Device: "port0"}

	source := &spyMetricsSource{metrics: structural, ruleMetrics: rules}
	service := acl.NewMetricsService(source)

	response, err := service.GetMetricsRules(t.Context(), request)

	require.NoError(t, err)
	require.Equal(t, request, source.receivedRule)
	require.Nil(t, source.receivedTags)
	require.Equal(t, rules, response.GetMetrics())
}
