package route_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// fakeHandle is an in-memory implementation of route.ModuleHandle.
type fakeHandle struct {
	routeCount uint64
	rangesV4   uint64
	rangesV6   uint64
	freed      bool
}

func (m *fakeHandle) DumpFIB() ([]croute.FIBEntry, error) {
	return nil, nil
}

func (m *fakeHandle) RouteCount() uint64 {
	return m.routeCount
}

func (m *fakeHandle) FIBRangeCountV4() uint64 {
	return m.rangesV4
}

func (m *fakeHandle) FIBRangeCountV6() uint64 {
	return m.rangesV6
}

func (m *fakeHandle) Free() {
	m.freed = true
}

// fakeBackend is an in-memory implementation of route.Backend.
type fakeBackend struct {
	mu       sync.Mutex
	handle   *fakeHandle
	counters []route.CounterView
	// queried records the counter names the service asked for.
	queried []string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{handle: &fakeHandle{}}
}

func (m *fakeBackend) UpdateModule(name string, entries []*routepb.FIBEntry) (route.ModuleHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.handle, nil
}

func (m *fakeBackend) DeleteModule(name string) error {
	return nil
}

func (m *fakeBackend) ModuleCounters(name string, counterNames []string) []route.CounterView {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queried = counterNames
	return m.counters
}

// newServiceWithConfig returns a service holding a single applied config
// named "cfg", backed by the supplied backend.
func newServiceWithConfig(t *testing.T, backend route.Backend) *route.RouteService {
	t.Helper()

	service := route.NewRouteService(backend)
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{ModuleName: "cfg"})
	require.NoError(t, err)

	return service
}

// counterView builds a dataplane counter reading for the fixed position used
// throughout these tests.
func counterView(name string, values [][]uint64) route.CounterView {
	return route.CounterView{
		Device:   "dev0",
		Pipeline: "pipe0",
		Function: "func0",
		Chain:    "chain0",
		Name:     name,
		Values:   values,
	}
}

// labelsOf returns the metric's labels as a name-to-value map.
func labelsOf(metric *commonpb.Metric) map[string]string {
	result := map[string]string{}
	for _, label := range metric.GetLabels() {
		result[label.GetName()] = label.GetValue()
	}

	return result
}

// findMetrics returns every emitted series carrying the given metric name.
func findMetrics(all []*commonpb.Metric, name string) []*commonpb.Metric {
	var result []*commonpb.Metric
	for _, metric := range all {
		if metric.GetName() == name {
			result = append(result, metric)
		}
	}

	return result
}

// requireMetric returns the single series with the given name whose labels
// contain every entry of want.
func requireMetric(t *testing.T, all []*commonpb.Metric, name string, want map[string]string) *commonpb.Metric {
	t.Helper()

	var matches []*commonpb.Metric
	for _, metric := range findMetrics(all, name) {
		labels := labelsOf(metric)
		matched := true
		for key, value := range want {
			if labels[key] != value {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, metric)
		}
	}

	require.Len(t, matches, 1, "expected exactly one %q series with labels %v", name, want)
	return matches[0]
}

// TestDataplaneCounterMapping verifies that each counter the route dataplane
// registers lands on the agreed metric family, reason, and address family.
func TestDataplaneCounterMapping(t *testing.T) {
	tests := []struct {
		counterName string
		metricName  string
		reason      string
		family      string
	}{
		{"route_forwarded_v4", "route_forwarded", "", "v4"},
		{"route_forwarded_v6", "route_forwarded", "", "v6"},
		{"route_drop_no_route_v4", "route_drop", "no_route", "v4"},
		{"route_drop_no_route_v6", "route_drop", "no_route", "v6"},
		{"route_drop_ttl_expired_v4", "route_drop", "ttl_expired", "v4"},
		{"route_drop_ttl_expired_v6", "route_drop", "ttl_expired", "v6"},
		{"route_drop_non_ip", "route_drop", "non_ip", "unknown"},
		{"route_drop_empty_route_list_v4", "route_drop", "empty_route_list", "v4"},
		{"route_drop_empty_route_list_v6", "route_drop", "empty_route_list", "v6"},
		{"route_drop_device_unresolved_v4", "route_drop", "device_unresolved", "v4"},
		{"route_drop_device_unresolved_v6", "route_drop", "device_unresolved", "v6"},
	}

	for _, test := range tests {
		t.Run(test.counterName, func(t *testing.T) {
			backend := newFakeBackend()
			backend.counters = []route.CounterView{
				counterView(test.counterName, [][]uint64{{7, 700}}),
			}

			service := newServiceWithConfig(t, backend)

			all, err := service.Metrics()
			require.NoError(t, err)

			wantLabels := map[string]string{
				"config":   "cfg",
				"device":   "dev0",
				"pipeline": "pipe0",
				"function": "func0",
				"chain":    "chain0",
				"family":   test.family,
			}
			if test.reason != "" {
				wantLabels["reason"] = test.reason
			}

			packets := requireMetric(t, all, test.metricName+"_packets", wantLabels)
			require.Equal(t, uint64(7), packets.GetCounter())

			bytes := requireMetric(t, all, test.metricName+"_bytes", wantLabels)
			require.Equal(t, uint64(700), bytes.GetCounter())

			// A forwarded counter carries no drop reason at all,
			// rather than an empty one.
			if test.reason == "" {
				require.NotContains(t, labelsOf(packets), "reason")
			}
		})
	}
}

// TestDataplaneCounterQueryCoversContract verifies that the service asks the
// dataplane for every counter it knows how to map.
func TestDataplaneCounterQueryCoversContract(t *testing.T) {
	backend := newFakeBackend()
	service := newServiceWithConfig(t, backend)

	_, err := service.Metrics()
	require.NoError(t, err)

	require.ElementsMatch(t, []string{
		"route_forwarded_v4",
		"route_forwarded_v6",
		"route_drop_no_route_v4",
		"route_drop_no_route_v6",
		"route_drop_ttl_expired_v4",
		"route_drop_ttl_expired_v6",
		"route_drop_non_ip",
		"route_drop_empty_route_list_v4",
		"route_drop_empty_route_list_v6",
		"route_drop_device_unresolved_v4",
		"route_drop_device_unresolved_v6",
	}, backend.queried)
}

// TestDataplaneMetricsEmitZeroCounters verifies that a counter reading zero is
// still exported, so a canary that is meant to stay at zero remains visible
// and rate() never sees the series vanish.
func TestDataplaneMetricsEmitZeroCounters(t *testing.T) {
	backend := newFakeBackend()
	backend.counters = []route.CounterView{
		counterView("route_drop_empty_route_list_v4", [][]uint64{{0, 0}}),
		counterView("route_forwarded_v6", [][]uint64{{0, 0}, {0, 0}}),
	}

	service := newServiceWithConfig(t, backend)

	all, err := service.Metrics()
	require.NoError(t, err)

	drop := requireMetric(t, all, "route_drop_packets", map[string]string{
		"reason": "empty_route_list",
		"family": "v4",
	})
	require.Equal(t, uint64(0), drop.GetCounter())

	forwarded := requireMetric(t, all, "route_forwarded_bytes", map[string]string{
		"family": "v6",
	})
	require.Equal(t, uint64(0), forwarded.GetCounter())
}

// TestDataplaneMetricsSumWorkerInstances verifies that the per-worker slots of
// a counter are added up into a single series.
func TestDataplaneMetricsSumWorkerInstances(t *testing.T) {
	backend := newFakeBackend()
	backend.counters = []route.CounterView{
		counterView("route_forwarded_v4", [][]uint64{{1, 100}, {2, 200}, {3, 300}}),
	}

	service := newServiceWithConfig(t, backend)

	all, err := service.Metrics()
	require.NoError(t, err)

	packets := requireMetric(t, all, "route_forwarded_packets", map[string]string{"family": "v4"})
	require.Equal(t, uint64(6), packets.GetCounter())

	bytes := requireMetric(t, all, "route_forwarded_bytes", map[string]string{"family": "v4"})
	require.Equal(t, uint64(600), bytes.GetCounter())
}

// requireConfigGauges asserts the three per-config gauges of "cfg" read back
// the supplied IPv4 range, IPv6 range, and nexthop counts.
func requireConfigGauges(t *testing.T, service *route.RouteService, rangesV4, rangesV6, nexthopCount float64) {
	t.Helper()

	all, err := service.Metrics()
	require.NoError(t, err)

	v4 := requireMetric(t, all, "route_fib_entries", map[string]string{"config": "cfg", "family": "v4"})
	require.Equal(t, rangesV4, v4.GetGauge())

	v6 := requireMetric(t, all, "route_fib_entries", map[string]string{"config": "cfg", "family": "v6"})
	require.Equal(t, rangesV6, v6.GetGauge())

	nexthops := requireMetric(t, all, "route_nexthops", map[string]string{"config": "cfg"})
	require.Equal(t, nexthopCount, nexthops.GetGauge())
}

// TestConfigGauges verifies that the FIB size is reported per address family
// and that the hardware nexthop count is reported once per config.
func TestConfigGauges(t *testing.T) {
	backend := newFakeBackend()
	backend.handle = &fakeHandle{routeCount: 5, rangesV4: 11, rangesV6: 23}

	service := newServiceWithConfig(t, backend)

	requireConfigGauges(t, service, 11, 23, 5)

	all, err := service.Metrics()
	require.NoError(t, err)
	nexthops := requireMetric(t, all, "route_nexthops", map[string]string{"config": "cfg"})
	require.NotContains(t, labelsOf(nexthops), "family")
}

// TestConfigGaugesMeasuredAtApply verifies that the gauges are measured once
// when the config is applied, rather than read off the handle on every
// scrape.
func TestConfigGaugesMeasuredAtApply(t *testing.T) {
	backend := newFakeBackend()
	handle := &fakeHandle{routeCount: 5, rangesV4: 11, rangesV6: 23}
	backend.handle = handle

	service := newServiceWithConfig(t, backend)
	requireConfigGauges(t, service, 11, 23, 5)

	// A published FIB never changes, so a scrape must not consult the
	// handle again. Moving the counts underneath the service is the only
	// way to observe whether it does.
	handle.routeCount = 6
	handle.rangesV4 = 12
	handle.rangesV6 = 24

	requireConfigGauges(t, service, 11, 23, 5)
}

// TestConfigGaugesFollowLatestApply verifies that re-applying a FIB retires
// the previous handle and republishes the gauges from the new one.
func TestConfigGaugesFollowLatestApply(t *testing.T) {
	backend := newFakeBackend()
	first := &fakeHandle{routeCount: 5, rangesV4: 11, rangesV6: 23}
	backend.handle = first

	service := newServiceWithConfig(t, backend)
	requireConfigGauges(t, service, 11, 23, 5)

	backend.handle = &fakeHandle{routeCount: 1, rangesV4: 2, rangesV6: 3}
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{ModuleName: "cfg"})
	require.NoError(t, err)

	requireConfigGauges(t, service, 2, 3, 1)
	require.True(t, first.freed, "the retired handle must be freed")
}

// TestConfigGaugesFollowConfigLifetime verifies that the per-config gauges
// appear once a FIB is applied, do not outlive their config, and carry
// nothing over from a deleted config of the same name.
func TestConfigGaugesFollowConfigLifetime(t *testing.T) {
	backend := newFakeBackend()
	backend.handle = &fakeHandle{routeCount: 5, rangesV4: 11, rangesV6: 23}
	service := route.NewRouteService(backend)

	all, err := service.Metrics()
	require.NoError(t, err)
	require.Empty(t, findMetrics(all, "route_config_updated_timestamp_seconds"))
	require.Empty(t, findMetrics(all, "route_fib_entries"))

	_, err = service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{ModuleName: "cfg"})
	require.NoError(t, err)

	all, err = service.Metrics()
	require.NoError(t, err)
	updated := requireMetric(t, all, "route_config_updated_timestamp_seconds", map[string]string{"config": "cfg"})
	require.Positive(t, updated.GetGauge())

	_, err = service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "cfg"})
	require.NoError(t, err)

	all, err = service.Metrics()
	require.NoError(t, err)
	require.Empty(t, findMetrics(all, "route_config_updated_timestamp_seconds"))
	require.Empty(t, findMetrics(all, "route_fib_entries"))
	require.Empty(t, findMetrics(all, "route_nexthops"))

	backend.handle = &fakeHandle{routeCount: 1, rangesV4: 2, rangesV6: 3}
	_, err = service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{ModuleName: "cfg"})
	require.NoError(t, err)

	requireConfigGauges(t, service, 2, 3, 1)
}

// TestShowFIBUnknownConfig verifies that ShowFIB reports NotFound for a
// config name that was never applied, distinguishing it from a registered
// config that genuinely holds no FIB entries.
func TestShowFIBUnknownConfig(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	_, err := service.ShowFIB(t.Context(), &routepb.ShowFIBRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestShowFIBEmptyConfig verifies that a registered config with no FIB
// entries still returns a normal empty success.
func TestShowFIBEmptyConfig(t *testing.T) {
	backend := newFakeBackend()
	service := newServiceWithConfig(t, backend)

	response, err := service.ShowFIB(t.Context(), &routepb.ShowFIBRequest{Name: "cfg"})
	require.NoError(t, err)
	require.Empty(t, response.GetEntries())
}
