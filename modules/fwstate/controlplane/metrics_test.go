package fwstate

import (
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// baseLabels is the shared label set used by collectDataplaneMetrics for every
// fwstate counter.
func baseLabels() []*commonpb.Label {
	return []*commonpb.Label{
		{Name: "config", Value: "cfg"},
		{Name: "device", Value: "dev0"},
		{Name: "pipeline", Value: "pl"},
		{Name: "function", Value: "fn"},
		{Name: "chain", Value: "ch"},
	}
}

// findMetric returns the first metric with the given name, or nil.
func findMetric(t *testing.T, metrics []*commonpb.Metric, name string) *commonpb.Metric {
	t.Helper()
	for _, m := range metrics {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// labelValue returns the value of the named label, or "" if absent.
func labelValue(m *commonpb.Metric, name string) string {
	for _, l := range m.Labels {
		if l.Name == name {
			return l.Value
		}
	}
	return ""
}

// counterValue returns the counter payload of m, failing the test if m is not a
// counter metric.
func counterValue(t *testing.T, m *commonpb.Metric) uint64 {
	t.Helper()
	require.NotNil(t, m, "metric not found")
	c, ok := m.Value.(*commonpb.Metric_Counter)
	require.True(t, ok, "metric %q is not a counter", m.Name)
	return c.Counter
}

// TestEmitCounterMetricsKnownCounters checks that the well-known fwstate
// counters are exported under their dedicated metric names with the base label
// set and without a "counter" label.
func TestEmitCounterMetricsKnownCounters(t *testing.T) {
	cases := []struct {
		name        string
		counterName string
		packets     uint64
		bytes       uint64
		wantNames   []string
	}{
		{
			name:        "fwstate_sync two-dimensional",
			counterName: "fwstate_sync",
			packets:     10,
			bytes:       1000,
			wantNames:   []string{"fwstate_sync_packets", "fwstate_sync_bytes"},
		},
		{
			name:        "fwstate_passthrough two-dimensional",
			counterName: "fwstate_passthrough",
			packets:     5,
			bytes:       500,
			wantNames:   []string{"fwstate_passthrough_packets", "fwstate_passthrough_bytes"},
		},
		{
			name:        "fwstate_external_dropped two-dimensional",
			counterName: "fwstate_external_dropped",
			packets:     3,
			bytes:       300,
			wantNames:   []string{"fwstate_external_dropped_packets", "fwstate_external_dropped_bytes"},
		},
		{
			name:        "fwstate_internal_forwarded two-dimensional",
			counterName: "fwstate_internal_forwarded",
			packets:     7,
			bytes:       700,
			wantNames:   []string{"fwstate_internal_forwarded_packets", "fwstate_internal_forwarded_bytes"},
		},
		{
			name:        "fwstate_sync_v4_inserted one-dimensional entries",
			counterName: "fwstate_sync_v4_inserted",
			packets:     42,
			wantNames:   []string{"fwstate_sync_v4_inserted_entries"},
		},
		{
			name:        "fwstate_sync_v6_inserted one-dimensional entries",
			counterName: "fwstate_sync_v6_inserted",
			packets:     43,
			wantNames:   []string{"fwstate_sync_v6_inserted_entries"},
		},
		{
			name:        "fwstate_sync_v4_insert_failed one-dimensional entries",
			counterName: "fwstate_sync_v4_insert_failed",
			packets:     1,
			wantNames:   []string{"fwstate_sync_v4_insert_failed_entries"},
		},
		{
			name:        "fwstate_sync_v6_insert_failed one-dimensional entries",
			counterName: "fwstate_sync_v6_insert_failed",
			packets:     2,
			wantNames:   []string{"fwstate_sync_v6_insert_failed_entries"},
		},
		{
			name:        "fwstate_sync_v4_suppressed one-dimensional entries",
			counterName: "fwstate_sync_v4_suppressed",
			packets:     8,
			wantNames:   []string{"fwstate_sync_v4_suppressed_entries"},
		},
		{
			name:        "fwstate_sync_v6_suppressed one-dimensional entries",
			counterName: "fwstate_sync_v6_suppressed",
			packets:     9,
			wantNames:   []string{"fwstate_sync_v6_suppressed_entries"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counter := ffi.CounterInfo{
				Name:   tc.counterName,
				Values: [][]uint64{{tc.packets, tc.bytes}},
			}

			metrics := emitCounterMetrics(counter, baseLabels())
			require.Len(t, metrics, len(tc.wantNames))

			for _, wantName := range tc.wantNames {
				m := findMetric(t, metrics, wantName)
				require.NotNil(t, m, "expected metric %q", wantName)
				// Known counters must not carry the generic "counter" label.
				require.Equal(t, "", labelValue(m, "counter"))
			}
		})
	}
}

// TestEmitCounterMetricsGenericModuleCounters checks that the generic
// per-module counters registered by cp_module_init (rx, tx, drop,
// pending_input, pending_output) are exported under their own dedicated metric
// names as [packets, bytes] pairs and without a "counter" label.
func TestEmitCounterMetricsGenericModuleCounters(t *testing.T) {
	cases := []struct {
		name        string
		counterName string
		packets     uint64
		bytes       uint64
		wantNames   []string
	}{
		{
			name:        "rx",
			counterName: "rx",
			packets:     11,
			bytes:       1100,
			wantNames:   []string{"fwstate_rx_packets", "fwstate_rx_bytes"},
		},
		{
			name:        "tx",
			counterName: "tx",
			packets:     22,
			bytes:       2200,
			wantNames:   []string{"fwstate_tx_packets", "fwstate_tx_bytes"},
		},
		{
			name:        "drop",
			counterName: "drop",
			packets:     4,
			bytes:       400,
			wantNames:   []string{"fwstate_drop_packets", "fwstate_drop_bytes"},
		},
		{
			name:        "pending_input",
			counterName: "pending_input",
			packets:     5,
			bytes:       500,
			wantNames:   []string{"fwstate_pending_input_packets", "fwstate_pending_input_bytes"},
		},
		{
			name:        "pending_output",
			counterName: "pending_output",
			packets:     6,
			bytes:       600,
			wantNames:   []string{"fwstate_pending_output_packets", "fwstate_pending_output_bytes"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counter := ffi.CounterInfo{
				Name:   tc.counterName,
				Values: [][]uint64{{tc.packets, tc.bytes}},
			}

			metrics := emitCounterMetrics(counter, baseLabels())
			require.Len(t, metrics, len(tc.wantNames))

			for _, wantName := range tc.wantNames {
				m := findMetric(t, metrics, wantName)
				require.NotNil(t, m, "expected metric %q", wantName)
				// Dedicated counters must not carry the generic "counter" label.
				require.Equal(t, "", labelValue(m, "counter"))
				// Base labels must be preserved.
				require.Equal(t, "cfg", labelValue(m, "config"))
				require.Equal(t, "dev0", labelValue(m, "device"))
			}
		})
	}
}

// TestEmitCounterMetricsGenericCounters checks that counters not in the
// well-known set are exported through the generic pair labelled with the
// counter name instead of being silently dropped.
func TestEmitCounterMetricsGenericCounters(t *testing.T) {
	cases := []struct {
		name        string
		counterName string
		packets     uint64
		bytes       uint64
	}{
		{name: "future_counter", counterName: "fwstate_some_future", packets: 9, bytes: 90},
		{name: "unknown", counterName: "something_unexpected", packets: 3, bytes: 30},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counter := ffi.CounterInfo{
				Name:   tc.counterName,
				Values: [][]uint64{{tc.packets, tc.bytes}},
			}

			metrics := emitCounterMetrics(counter, baseLabels())
			require.Len(t, metrics, 2)

			pkts := findMetric(t, metrics, "fwstate_counter_packets")
			byts := findMetric(t, metrics, "fwstate_counter_bytes")

			require.Equal(t, tc.packets, counterValue(t, pkts))
			require.Equal(t, tc.bytes, counterValue(t, byts))

			// The generic pair must carry the original counter name as a
			// "counter" label so the series can be distinguished.
			require.Equal(t, tc.counterName, labelValue(pkts, "counter"))
			require.Equal(t, tc.counterName, labelValue(byts, "counter"))

			// Base labels must be preserved.
			require.Equal(t, "cfg", labelValue(pkts, "config"))
			require.Equal(t, "dev0", labelValue(pkts, "device"))
		})
	}
}

// TestEmitCounterMetricsZeroSuppression checks that a counter whose worker
// values are all zero is omitted entirely (no series emitted).
func TestEmitCounterMetricsZeroSuppression(t *testing.T) {
	cases := []string{
		"fwstate_sync",
		"rx",
		"drop",
		"pending_input",
		"fwstate_some_future",
	}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			counter := ffi.CounterInfo{
				Name:   name,
				Values: [][]uint64{{0, 0}, {0, 0}},
			}
			require.Empty(t, emitCounterMetrics(counter, baseLabels()))
		})
	}
}

// TestEmitCounterMetricsAggregatesWorkers checks that values from multiple
// workers are summed before being exported.
func TestEmitCounterMetricsAggregatesWorkers(t *testing.T) {
	counter := ffi.CounterInfo{
		Name: "fwstate_sync",
		Values: [][]uint64{
			{10, 100},
			{5, 50},
			{0, 0},
		},
	}

	metrics := emitCounterMetrics(counter, baseLabels())
	require.Equal(t, uint64(15), counterValue(t, findMetric(t, metrics, "fwstate_sync_packets")))
	require.Equal(t, uint64(150), counterValue(t, findMetric(t, metrics, "fwstate_sync_bytes")))
}

// stubMetricsSource is a canned metrics source that returns a fixed
// series list and records the tags it was queried with.
type stubMetricsSource struct {
	series  []*commonpb.Metric
	gotTags []*commonpb.MetricTag
}

func (m *stubMetricsSource) Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	m.gotTags = tags
	return m.series, nil
}

// Test_MetricsService_AggregatesAllSources verifies that the module's
// metrics RPC merges every source's series and forwards the request's
// tags to each source.
func Test_MetricsService_AggregatesAllSources(t *testing.T) {
	fwstateSource := &stubMetricsSource{series: []*commonpb.Metric{
		commonpb.NewMetricCounter("fwstate_rx_packets", 3,
			&commonpb.Label{Name: "config", Value: "fw0"},
		),
	}}
	mapSource := &stubMetricsSource{series: []*commonpb.Metric{
		commonpb.NewMetricCounter("grpc_server_started_total", 1,
			&commonpb.Label{Name: "grpc_service", Value: "fwstatemap"},
		),
	}}

	service := NewMetricsService(fwstateSource, mapSource)
	tags := []*commonpb.MetricTag{{Name: "config", Value: "fw0"}}
	response, err := service.GetMetrics(t.Context(), &commonpb.GetMetricsRequest{Tags: tags})
	require.NoError(t, err)

	names := make([]string, 0, len(response.GetMetrics()))
	for _, metric := range response.GetMetrics() {
		names = append(names, metric.Name)
	}
	require.ElementsMatch(t, []string{"fwstate_rx_packets", "grpc_server_started_total"}, names)
	require.Equal(t, tags, fwstateSource.gotTags)
	require.Equal(t, tags, mapSource.gotTags)
}
