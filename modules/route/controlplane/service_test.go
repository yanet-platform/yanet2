package route_test

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// fakeModuleHandle is an in-memory implementation of route.ModuleHandle.
type fakeModuleHandle struct {
	backend *fakeBackend
	name    string
	devices []string

	// publishErr, when set, is returned by the next Publish call instead
	// of taking effect, then cleared.
	publishErr error
	published  bool

	// freeRefusals counts down on every Free call, reporting
	// ffi.ErrStillReferenced while it is positive.
	freeRefusals int
	// freed records whether the handle was ever actually released.
	freed bool
	// freeCount counts every successful release, catching an accidental
	// double free.
	freeCount int
}

func (m *fakeModuleHandle) Publish() error {
	if m.publishErr != nil {
		err := m.publishErr
		m.publishErr = nil
		return err
	}
	m.published = true
	m.backend.recordModulePublish(m.name, m.devices)
	return nil
}

func (m *fakeModuleHandle) Free() error {
	if m.freeRefusals > 0 {
		m.freeRefusals--
		return ffi.ErrStillReferenced
	}
	m.freed = true
	m.freeCount++
	m.backend.recordEvent("free module")
	return nil
}

// fakeFIBHandle is an in-memory implementation of route.FIBHandle.
type fakeFIBHandle struct {
	backend             *fakeBackend
	name                string
	routeCount          uint64
	rangesV4            uint64
	rangesV6            uint64
	nexthopCounterNames []string
	// activeNamesErr, when set, is returned by ActiveNexthopCounterNames
	// instead of nexthopCounterNames, standing in for the control-plane
	// OOM that is the only realistic way that call fails.
	activeNamesErr error

	// publishErr, when set, is returned by the next Publish call instead
	// of taking effect, then cleared.
	publishErr error
	published  bool

	// freeRefusals counts down on every Free call, reporting
	// ffi.ErrStillReferenced while it is positive.
	freeRefusals int
	// freed records whether the handle was ever actually released.
	freed bool
	// freeCount counts every successful release, catching an accidental
	// double free.
	freeCount int
}

func (m *fakeFIBHandle) Publish() error {
	if m.publishErr != nil {
		err := m.publishErr
		m.publishErr = nil
		return err
	}
	m.published = true
	m.backend.recordFIBPublish(m.name)
	return nil
}

func (m *fakeFIBHandle) ActiveNexthopCounterNames() ([]string, error) {
	if m.activeNamesErr != nil {
		return nil, m.activeNamesErr
	}
	return m.nexthopCounterNames, nil
}

func (m *fakeFIBHandle) RouteCount() uint64 {
	return m.routeCount
}

func (m *fakeFIBHandle) FIBRangeCountV4() uint64 {
	return m.rangesV4
}

func (m *fakeFIBHandle) FIBRangeCountV6() uint64 {
	return m.rangesV6
}

func (m *fakeFIBHandle) Free() error {
	if m.freeRefusals > 0 {
		m.freeRefusals--
		return ffi.ErrStillReferenced
	}
	m.freed = true
	m.freeCount++
	m.backend.recordEvent("free fib")
	return nil
}

var (
	_ route.ModuleHandle = (*fakeModuleHandle)(nil)
	_ route.FIBHandle    = (*fakeFIBHandle)(nil)
)

// newModuleCall records one NewModule call's arguments.
type newModuleCall struct {
	name    string
	devices []string
}

// newFIBCall records one NewFIB call's arguments.
type newFIBCall struct {
	name    string
	devices []string
	entries []*routepb.FIBEntry
}

// fakeBackend is an in-memory implementation of route.Backend.
//
// modulePublished / moduleTable / fibPublished mirror what Published
// reports for a name: a build (NewModule / NewFIB) never touches them, a
// handle's own Publish call does. events is the cross-handle publish and
// free order every handle this backend ever issued appends to, so a test
// can assert the exact interleaving UpdateFIB produces.
type fakeBackend struct {
	mu sync.Mutex

	modulePublished map[string]bool
	moduleTable     map[string][]string
	fibPublished    map[string]bool

	// nextModuleHandle / nextFIBHandle, when set for a name, are
	// returned (and then cleared) by the next NewModule / NewFIB call
	// for that name; otherwise a fresh default handle is created.
	nextModuleHandle map[string]*fakeModuleHandle
	nextFIBHandle    map[string]*fakeFIBHandle
	// lastModuleHandle / lastFIBHandle record the most recently issued
	// handle per name, so a test can inspect one it never injected.
	lastModuleHandle map[string]*fakeModuleHandle
	lastFIBHandle    map[string]*fakeFIBHandle

	// newFIBErr, when set, is returned by the next NewFIB call itself
	// (a build failure) instead of a handle, then cleared.
	newFIBErr error

	// dumpEntries holds the injected DumpFIB result per name; a fib
	// handle's Publish never derives it from the entries it was built
	// from, so a test wanting ShowFIB to render something sets it
	// directly.
	dumpEntries map[string][]croute.FIBEntry

	// calls records the ordered build/delete backend call log per name,
	// e.g. []string{"module", "fib"}.
	calls map[string][]string
	// events records the ordered publish/free log shared across every
	// handle this backend issued, e.g. []string{"publish fib", "publish
	// module"}.
	events []string

	newModuleCalls    []newModuleCall
	newFIBCalls       []newFIBCall
	deleteModuleCalls []string
	deleteFIBCalls    []string

	counters []route.CounterView
	// nexthopCounters backs NexthopCounters, the per-nexthop read.
	nexthopCounters []route.CounterView
	// queries records the counterNames argument of every ModuleCounters
	// call, in call order.
	queries [][]string
	// nexthopQueries records the counterNames argument of every
	// NexthopCounters call, in call order.
	nexthopQueries [][]string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		modulePublished:  map[string]bool{},
		moduleTable:      map[string][]string{},
		fibPublished:     map[string]bool{},
		nextModuleHandle: map[string]*fakeModuleHandle{},
		nextFIBHandle:    map[string]*fakeFIBHandle{},
		lastModuleHandle: map[string]*fakeModuleHandle{},
		lastFIBHandle:    map[string]*fakeFIBHandle{},
		dumpEntries:      map[string][]croute.FIBEntry{},
		calls:            map[string][]string{},
	}
}

var _ route.Backend = (*fakeBackend)(nil)

// seedRestart marks name as already published under devices, by both
// halves, without ever going through this backend's NewModule/NewFIB —
// standing in for a config an earlier control-plane process published.
func (m *fakeBackend) seedRestart(name string, devices []string) {
	m.modulePublished[name] = true
	m.moduleTable[name] = devices
	m.fibPublished[name] = true
}

func (m *fakeBackend) recordModulePublish(name string, table []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.modulePublished[name] = true
	m.moduleTable[name] = table
	m.events = append(m.events, "publish module")
}

func (m *fakeBackend) recordFIBPublish(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fibPublished[name] = true
	m.events = append(m.events, "publish fib")
}

func (m *fakeBackend) recordEvent(event string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.events = append(m.events, event)
}

func (m *fakeBackend) Published(name string) (route.Published, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.modulePublished[name] {
		return route.Published{}, fmt.Errorf("module %q: %w", name, ffi.ErrNotFound)
	}
	return route.Published{
		Devices: append([]string(nil), m.moduleTable[name]...),
		FIB:     m.fibPublished[name],
	}, nil
}

// NewModule mimics the real module: index 0 is always its own any-device
// entry (""), and it must not fail when devices already carries one back
// from a read-back table.
func (m *fakeBackend) NewModule(name string, devices []string) (route.ModuleHandle, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls[name] = append(m.calls[name], "module")
	m.newModuleCalls = append(m.newModuleCalls, newModuleCall{
		name:    name,
		devices: append([]string(nil), devices...),
	})

	table := []string{""}
	known := map[string]struct{}{"": {}}
	for _, device := range devices {
		if device == "" {
			continue
		}
		if _, ok := known[device]; ok {
			continue
		}
		known[device] = struct{}{}
		table = append(table, device)
	}

	handle, ok := m.nextModuleHandle[name]
	if !ok {
		handle = &fakeModuleHandle{}
	}
	delete(m.nextModuleHandle, name)
	handle.backend = m
	handle.name = name
	handle.devices = table
	m.lastModuleHandle[name] = handle

	return handle, table, nil
}

// NewFIB derives the returned handle's counter names straight off
// entries, without resolving overlaps: the fake has no LPM, so a test
// exercising shadowed-nexthop exclusion must go through the real backend
// instead.
func (m *fakeBackend) NewFIB(name string, devices []string, entries []*routepb.FIBEntry) (route.FIBHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls[name] = append(m.calls[name], "fib")
	m.newFIBCalls = append(m.newFIBCalls, newFIBCall{
		name:    name,
		devices: append([]string(nil), devices...),
		entries: entries,
	})

	if m.newFIBErr != nil {
		err := m.newFIBErr
		m.newFIBErr = nil
		return nil, err
	}

	handle, ok := m.nextFIBHandle[name]
	if !ok {
		handle = &fakeFIBHandle{}
	}
	delete(m.nextFIBHandle, name)
	handle.backend = m
	handle.name = name

	if handle.nexthopCounterNames == nil {
		names := map[string]struct{}{}
		for _, entry := range entries {
			for _, nh := range entry.GetNexthops() {
				if counter := nh.GetCounter(); counter != "" {
					names[counter] = struct{}{}
				}
			}
		}
		sorted := make([]string, 0, len(names))
		for name := range names {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		handle.nexthopCounterNames = sorted
	}
	m.lastFIBHandle[name] = handle

	return handle, nil
}

func (m *fakeBackend) DeleteModule(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls[name] = append(m.calls[name], "delete_module")
	m.deleteModuleCalls = append(m.deleteModuleCalls, name)

	if !m.modulePublished[name] {
		return fmt.Errorf("module %q: %w", name, ffi.ErrNotFound)
	}
	delete(m.modulePublished, name)
	delete(m.moduleTable, name)
	return nil
}

func (m *fakeBackend) DeleteFIB(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls[name] = append(m.calls[name], "delete_fib")
	m.deleteFIBCalls = append(m.deleteFIBCalls, name)

	if !m.fibPublished[name] {
		return fmt.Errorf("object %q: %w", name, ffi.ErrNotFound)
	}
	delete(m.fibPublished, name)
	delete(m.dumpEntries, name)
	return nil
}

func (m *fakeBackend) DumpFIB(name string) ([]croute.FIBEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.fibPublished[name] {
		return nil, fmt.Errorf("config %q: %w", name, ffi.ErrNotFound)
	}
	return m.dumpEntries[name], nil
}

func (m *fakeBackend) ModuleCounters(name string, counterNames []string) []route.CounterView {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queries = append(m.queries, append([]string(nil), counterNames...))
	return m.counters
}

func (m *fakeBackend) NexthopCounters(name string, counterNames []string) []route.CounterView {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nexthopQueries = append(m.nexthopQueries, append([]string(nil), counterNames...))
	return m.nexthopCounters
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

	// No nexthop counters registered, so this is collectDataplaneMetrics's
	// query alone.
	require.Len(t, backend.queries, 1)
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
	}, backend.queries[0])
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
	backend.nextFIBHandle["cfg"] = &fakeFIBHandle{routeCount: 5, rangesV4: 11, rangesV6: 23}

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
	handle := &fakeFIBHandle{routeCount: 5, rangesV4: 11, rangesV6: 23}
	backend.nextFIBHandle["cfg"] = handle

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
	first := &fakeFIBHandle{routeCount: 5, rangesV4: 11, rangesV6: 23}
	backend.nextFIBHandle["cfg"] = first

	service := newServiceWithConfig(t, backend)
	requireConfigGauges(t, service, 11, 23, 5)

	backend.nextFIBHandle["cfg"] = &fakeFIBHandle{routeCount: 1, rangesV4: 2, rangesV6: 3}
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
	backend.nextFIBHandle["cfg"] = &fakeFIBHandle{routeCount: 5, rangesV4: 11, rangesV6: 23}
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

	backend.nextFIBHandle["cfg"] = &fakeFIBHandle{routeCount: 1, rangesV4: 2, rangesV6: 3}
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

// TestDeleteConfigUnknownConfig verifies that DeleteConfig reports NotFound
// for a config name that was never registered.
func TestDeleteConfigUnknownConfig(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	_, err := service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "missing"})
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

// testDstMAC and testSrcMAC are the fixed EUI-48 addresses used across the
// nexthop-counter tests below.
var (
	testDstMAC = [6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x01}
	testSrcMAC = [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
)

// testNexthop builds a FIBNexthop carrying the fixed test MACs and the
// given device and counter.
func testNexthop(device, counter string) *routepb.FIBNexthop {
	return &routepb.FIBNexthop{
		DstMac:  commonpb.NewMACAddressEUI48(testDstMAC),
		SrcMac:  commonpb.NewMACAddressEUI48(testSrcMAC),
		Device:  device,
		Counter: counter,
	}
}

// testFIBEntry builds a single-address FIBEntry range covering the given
// prefix's network address, carrying nexthops.
func testFIBEntry(t *testing.T, prefix string, nexthops ...*routepb.FIBNexthop) *routepb.FIBEntry {
	t.Helper()

	addr := netip.MustParsePrefix(prefix).Addr()
	ipRange, err := commonpb.NewIPRange(addr, addr)
	require.NoError(t, err)

	return &routepb.FIBEntry{Range: ipRange, Nexthops: nexthops}
}

// TestUpdateFIBMaterializesEmptyCounter verifies that a nexthop which leaves
// counter empty is materialized to "nexthop_<device>_<dst_mac>" in what the
// backend receives.
func TestUpdateFIBMaterializesEmptyCounter(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err)

	require.Len(t, backend.newFIBCalls, 1)
	got := backend.newFIBCalls[0].entries[0].Nexthops[0]
	require.Equal(t, "nexthop_eth0_aabbccddee01", got.GetCounter())
}

// TestUpdateFIBPassesThroughExplicitCounter verifies that a nexthop's
// explicit counter reaches the backend verbatim, unmodified.
func TestUpdateFIBPassesThroughExplicitCounter(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_my_counter"))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err)

	got := backend.newFIBCalls[0].entries[0].Nexthops[0]
	require.Equal(t, "nexthop_my_counter", got.GetCounter())
}

// TestUpdateFIBDisabledRejectsExplicitCounter verifies that an explicit
// counter name is rejected as InvalidArgument when nexthop counters are
// disabled, and that the config is never applied to the backend.
func TestUpdateFIBDisabledRejectsExplicitCounter(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend, route.WithNexthopCountersDisabled())

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_my_counter"))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, backend.calls, "the backend must not be called when validation rejects the request")
}

// A FIB refused with ErrTooManyNexthops is a request error, not an internal
// failure.
func Test_RouteService_UpdateFIB_TooManyNexthopsIsInvalidArgument(t *testing.T) {
	backend := newFakeBackend()
	backend.newFIBErr = fmt.Errorf("wrapped: %w", route.ErrTooManyNexthops)
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestUpdateFIBDisabledAllowsEmptyCounter verifies that a nexthop leaving
// counter empty succeeds while nexthop counters are disabled, and stays
// empty rather than being materialized.
func TestUpdateFIBDisabledAllowsEmptyCounter(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend, route.WithNexthopCountersDisabled())

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err)

	got := backend.newFIBCalls[0].entries[0].Nexthops[0]
	require.Empty(t, got.GetCounter())
}

// TestUpdateFIBRejectsOverlongCounter verifies the accepted counter-name
// length boundary directly against literal byte counts, rather than through
// croute.CounterNameMaxLen, so an off-by-one in the constant itself cannot
// hide the regression.
func TestUpdateFIBRejectsOverlongCounter(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	okEntry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_"+strings.Repeat("a", 127-len("nexthop_"))))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{okEntry},
	})
	require.NoError(t, err, "a 127-byte counter name must be accepted")

	tooLongEntry := testFIBEntry(t, "10.0.0.1/32", testNexthop("eth0", "nexthop_"+strings.Repeat("a", 128-len("nexthop_"))))
	_, err = service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{tooLongEntry},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err), "a 128-byte counter name must be rejected")
}

// TestUpdateFIBRejectsCounterWithNULByte verifies that a counter name
// carrying an embedded NUL byte is rejected, rather than silently
// truncated at the C boundary where it would diverge from the name
// registered later.
func TestUpdateFIBRejectsCounterWithNULByte(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_ab\x00cd"))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, backend.calls, "the backend must not be called when validation rejects the request")
}

// TestUpdateFIBRejectsCounterWithoutPrefix verifies that an explicit
// counter name not starting with "nexthop_" is rejected, rather than being
// accepted and risking a future collision with a route-specific or generic
// module-level counter name.
func TestUpdateFIBRejectsCounterWithoutPrefix(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "route_forwarded_v4"))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, backend.calls, "the backend must not be called when validation rejects the request")
}

// TestUpdateFIBRejectsConflictingCounterNamesWithinEntry verifies that
// listing the same forwarding identity twice in one entry under two
// different counter names is rejected as InvalidArgument naming both
// names, and that nothing reaches the backend.
func TestUpdateFIBRejectsConflictingCounterNamesWithinEntry(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32",
		testNexthop("eth0", "nexthop_a"),
		testNexthop("eth0", "nexthop_b"),
	)

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.ErrorContains(t, err, "nexthop_a")
	require.ErrorContains(t, err, "nexthop_b")
	require.Empty(t, backend.calls, "the backend must not be called when validation rejects the request")
}

// TestUpdateFIBRejectsConflictingCounterNamesAcrossEntries verifies that the
// conflicting-counter check spans the whole request rather than one entry
// at a time.
func TestUpdateFIBRejectsConflictingCounterNamesAcrossEntries(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entryA := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_a"))
	entryB := testFIBEntry(t, "10.0.1.0/32", testNexthop("eth0", "nexthop_b"))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entryA, entryB},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, backend.calls, "the backend must not be called when validation rejects the request")
}

// TestUpdateFIBToleratesActiveNexthopCounterNamesFailure verifies that a
// failure reading back the reachable counter-name set off an
// already-published handle still reports success, since the FIB itself did
// apply, and leaves the handle live and tracked rather than freeing it.
func TestUpdateFIBToleratesActiveNexthopCounterNamesFailure(t *testing.T) {
	backend := newFakeBackend()
	handle := &fakeFIBHandle{routeCount: 3, activeNamesErr: errors.New("fib_iter_new: allocation failure")}
	backend.nextFIBHandle["cfg"] = handle
	backend.nexthopCounters = []route.CounterView{
		counterView("nexthop_my_counter", [][]uint64{{1, 100}}),
	}
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_my_counter"))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err, "the FIB itself did apply, so the RPC must still report success")
	require.False(t, handle.freed, "a published handle must never be freed on this path")

	requireConfigGauges(t, service, 0, 0, 3)

	all, err := service.Metrics()
	require.NoError(t, err)
	require.Empty(t, findMetrics(all, "route_nexthop_packets"), "the counter set must be empty until the next update")
	for _, query := range append(append([][]string(nil), backend.queries...), backend.nexthopQueries...) {
		require.NotContains(t, query, "nexthop_my_counter", "the empty counter set must never be queried for")
	}

	_, err = service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "cfg"})
	require.NoError(t, err)
	require.True(t, handle.freed, "the config must still be tracked so DeleteConfig can free it")
}

// TestNexthopMetricsSumWorkerInstances verifies that collectNexthopMetrics
// reaches the dataplane through RouteService.Metrics and sums a registered
// nexthop counter's worker instances into one series carrying the full
// label set, rather than only exercising resolveNexthopCounters.
func TestNexthopMetricsSumWorkerInstances(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_my_counter"))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err)

	backend.nexthopCounters = []route.CounterView{
		counterView("nexthop_my_counter", [][]uint64{{1, 100}, {2, 200}}),
	}

	all, err := service.Metrics()
	require.NoError(t, err)

	wantLabels := map[string]string{
		"config":   "cfg",
		"device":   "dev0",
		"pipeline": "pipe0",
		"function": "func0",
		"chain":    "chain0",
		"counter":  "nexthop_my_counter",
	}

	packets := requireMetric(t, all, "route_nexthop_packets", wantLabels)
	require.Equal(t, uint64(3), packets.GetCounter())

	bytes := requireMetric(t, all, "route_nexthop_bytes", wantLabels)
	require.Equal(t, uint64(300), bytes.GetCounter())
}

// TestNexthopMetricsExactTagNeverReadsForeignCounter verifies that an exact
// "counter" tag naming anything outside a config's own reachable names never
// reaches collectNexthopMetrics's dataplane read, so a foreign counter can
// never resurface mislabeled under the route_nexthop_* family.
func TestNexthopMetricsExactTagNeverReadsForeignCounter(t *testing.T) {
	backend := newFakeBackend()
	backend.nexthopCounters = []route.CounterView{
		counterView("route_forwarded_v4", [][]uint64{{7, 700}}),
	}
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_my_counter"))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err)

	tag := &commonpb.MetricTag{Name: "counter", Value: "route_forwarded_v4"}
	all, err := service.Metrics(tag)
	require.NoError(t, err)

	require.Empty(t, findMetrics(all, "route_nexthop_packets"))
	require.Empty(t, findMetrics(all, "route_nexthop_bytes"))

	// The "route_forwarded_v4" name identifies a module-level counter, which
	// carries no "counter" label, so neither collectNexthopMetrics (whose only
	// reachable name is "nexthop_my_counter") nor collectDataplaneMetrics
	// (whose exact-tag branch never matches a per-entry read) has anything
	// left to query.
	require.Empty(t, backend.queries)
	require.Empty(t, backend.nexthopQueries)
}

// The first UpdateFIB of a name links the request's devices in first-seen
// order and publishes the object before the module.
func Test_RouteService_UpdateFIB_FirstUpdatePublishesObjectThenModule(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entryA := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth1", ""))
	entryB := testFIBEntry(t, "10.0.1.0/32", testNexthop("eth0", ""))

	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entryA, entryB},
	})
	require.NoError(t, err)

	require.Equal(t, []string{"module", "fib"}, backend.calls["cfg"], "the module must be built before the object")
	require.Equal(t, []string{"publish fib", "publish module"}, backend.events, "the object must publish before the module")
	require.Len(t, backend.newModuleCalls, 1)
	require.Equal(t, []string{"eth1", "eth0"}, backend.newModuleCalls[0].devices,
		"devices must be linked in first-seen order")
	require.Len(t, backend.newFIBCalls, 1)
	require.Equal(t, []string{"", "eth1", "eth0"}, backend.newFIBCalls[0].devices,
		"the object must be built from the table the module handed back, index 0 being its own entry")
}

// An UpdateFIB naming only known devices publishes the object alone and
// keeps the module.
func Test_RouteService_UpdateFIB_KnownDevicesRepublishesObjectAlone(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	first := &fakeFIBHandle{}
	backend.nextFIBHandle["cfg"] = first
	entry1 := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry1},
	})
	require.NoError(t, err)

	entry2 := testFIBEntry(t, "10.0.1.0/32", testNexthop("eth0", ""))
	_, err = service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry2},
	})
	require.NoError(t, err)

	require.Equal(t, []string{"module", "fib", "fib"}, backend.calls["cfg"],
		"the module must not be rebuilt when its table already covers every named device")
	require.Equal(t,
		[]string{"publish fib", "publish module", "publish fib", "free fib"},
		backend.events,
		"the second update publishes only the object and frees the retired one",
	)
	require.True(t, first.freed, "the retired object must be freed")
	require.False(t, backend.lastModuleHandle["cfg"].freed, "the still-shared module must not be freed")
}

// A new device extends the table in place, republishes the module before
// the object and frees the superseded pair.
func Test_RouteService_UpdateFIB_NewDeviceGrowsAndRepublishesModule(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	entry1 := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry1},
	})
	require.NoError(t, err)
	firstModule := backend.lastModuleHandle["cfg"]
	firstObject := backend.lastFIBHandle["cfg"]

	entry2 := testFIBEntry(t, "10.0.1.0/32", testNexthop("eth1", ""))
	_, err = service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry2},
	})
	require.NoError(t, err)

	require.Len(t, backend.newModuleCalls, 2)
	require.Equal(t, []string{"eth0"}, backend.newModuleCalls[0].devices)
	require.Equal(t, []string{"", "eth0", "eth1"}, backend.newModuleCalls[1].devices,
		"the extended table, read back from Published, must keep eth0's index and append eth1")
	require.Equal(t,
		[]string{"publish fib", "publish module", "publish module", "publish fib", "free fib", "free module"},
		backend.events,
		"a grown table publishes the new module before the new object, then frees the superseded pair",
	)
	require.True(t, firstModule.freed, "the superseded module must be freed once its replacement is published")
	require.True(t, firstObject.freed, "the superseded object must be freed once its replacement is published")
}

// After a restart, known devices publish only the object and DeleteConfig
// still deletes both halves by name without a module handle.
func Test_RouteService_UpdateFIB_RestartKnownDevicesPublishesObjectOnly(t *testing.T) {
	backend := newFakeBackend()
	// Simulate a module and object published by an earlier control-plane
	// process: present in what Published reports, without ever going
	// through this backend instance's NewModule/NewFIB.
	backend.seedRestart("cfg", []string{"", "eth0"})
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err)

	require.Empty(t, backend.newModuleCalls, "the module is already published with a sufficient table")
	require.Equal(t, []string{"publish fib"}, backend.events)

	_, err = service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "cfg"})
	require.NoError(t, err)
	require.Equal(t, []string{"cfg"}, backend.deleteModuleCalls)
	require.Equal(t, []string{"cfg"}, backend.deleteFIBCalls)
}

// After a restart, a new device extends the published table and
// republishes the module before the object.
func Test_RouteService_UpdateFIB_RestartNewDeviceRepublishesModule(t *testing.T) {
	backend := newFakeBackend()
	backend.seedRestart("cfg", []string{"", "eth0"})
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth1", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err)

	require.Len(t, backend.newModuleCalls, 1)
	require.Equal(t, []string{"", "eth0", "eth1"}, backend.newModuleCalls[0].devices)
	require.Equal(t, []string{"publish module", "publish fib"}, backend.events,
		"an object already published means the rebuilt module must publish first")
}

// After a restart, an object publish failing after a grown module went out
// keeps the new module without an object, an earlier process's object not
// being ours to hold, and DeleteConfig still deletes both halves by name.
func Test_RouteService_UpdateFIB_RestartObjectPublishFailureAfterGrownModuleKeepsModule(t *testing.T) {
	backend := newFakeBackend()
	backend.seedRestart("cfg", []string{"", "eth0"})
	// The table the earlier process applied: two IPv4 ranges and one
	// IPv6 range over two counted nexthops, one of them shared.
	nexthopA := croute.FIBNexthop{DstMAC: net.HardwareAddr{0, 0, 0, 0, 0, 0xa}, SrcMAC: net.HardwareAddr{0, 0, 0, 0, 0, 1}, Device: "eth0", Counter: "nexthop_a"}
	nexthopB := croute.FIBNexthop{DstMAC: net.HardwareAddr{0, 0, 0, 0, 0, 0xb}, SrcMAC: net.HardwareAddr{0, 0, 0, 0, 0, 1}, Device: "eth0", Counter: "nexthop_b"}
	backend.dumpEntries["cfg"] = []croute.FIBEntry{
		{AddressFamily: croute.AddressFamilyIPv4, PrefixFrom: netip.MustParseAddr("10.0.0.0"), PrefixTo: netip.MustParseAddr("10.0.0.255"), Nexthops: []croute.FIBNexthop{nexthopA, nexthopB}},
		{AddressFamily: croute.AddressFamilyIPv4, PrefixFrom: netip.MustParseAddr("10.0.1.0"), PrefixTo: netip.MustParseAddr("10.0.1.255"), Nexthops: []croute.FIBNexthop{nexthopA}},
		{AddressFamily: croute.AddressFamilyIPv6, PrefixFrom: netip.MustParseAddr("fd00::"), PrefixTo: netip.MustParseAddr("fd00::ffff"), Nexthops: []croute.FIBNexthop{nexthopB}},
	}
	backend.nextFIBHandle["cfg"] = &fakeFIBHandle{publishErr: errors.New("boom")}
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth1", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.Error(t, err)

	newModule := backend.lastModuleHandle["cfg"]
	require.True(t, newModule.published, "the grown module published fine; only the object failed")
	require.False(t, newModule.freed, "the published module must stay with the entry")
	require.True(t, backend.lastFIBHandle["cfg"].freed, "the object that failed to publish must be discarded")

	// The table the dataplane keeps running is the earlier process's,
	// so its facts are read back off it and its apply time is not.
	requireConfigGauges(t, service, 2, 1, 2)
	all, err := service.Metrics()
	require.NoError(t, err)
	require.Empty(t, findMetrics(all, "route_config_updated_timestamp_seconds"))
	require.Contains(t, backend.nexthopQueries, []string{"nexthop_a", "nexthop_b"},
		"the counters of the table still running must keep being scraped")

	list, err := service.ListConfigs(t.Context(), &routepb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"cfg"}, list.GetConfigs())

	_, err = service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "cfg"})
	require.NoError(t, err)
	require.Equal(t, []string{"cfg"}, backend.deleteModuleCalls)
	require.Equal(t, []string{"cfg"}, backend.deleteFIBCalls)
	require.Equal(t, 1, newModule.freeCount, "the module must be freed exactly once")
}

// An object publish failing after a grown module went out keeps the old
// object under the new module and frees the superseded module.
func Test_RouteService_UpdateFIB_ObjectPublishFailureAfterGrownModuleKeepsOldObject(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	firstEntry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{firstEntry},
	})
	require.NoError(t, err)
	oldModule := backend.lastModuleHandle["cfg"]
	oldObject := backend.lastFIBHandle["cfg"]

	backend.nextFIBHandle["cfg"] = &fakeFIBHandle{publishErr: errors.New("boom")}
	newEntry := testFIBEntry(t, "10.0.1.0/32", testNexthop("eth1", ""))
	_, err = service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{newEntry},
	})
	require.Error(t, err)

	newModule := backend.lastModuleHandle["cfg"]
	require.NotSame(t, oldModule, newModule, "the module must have been rebuilt for the grown table")
	require.True(t, newModule.published, "the new module published fine; only the object failed")
	require.True(t, oldModule.freed, "the superseded module must be freed even though the update failed")
	require.False(t, oldObject.freed, "the old object must stay published, moved into the new entry")

	// The failed-to-publish object is discarded, not left dangling.
	failedObject := backend.lastFIBHandle["cfg"]
	require.True(t, failedObject.freed)

	_, err = service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "cfg"})
	require.NoError(t, err)
	require.Equal(t, 1, newModule.freeCount, "the new module must be freed exactly once")
	require.Equal(t, 1, oldObject.freeCount, "the old object, now the entry's, must be freed exactly once")
}

// A first object publish failing leaves nothing published and nothing
// tracked.
func Test_RouteService_UpdateFIB_FirstPublishFailurePublishesNothing(t *testing.T) {
	backend := newFakeBackend()
	backend.nextFIBHandle["cfg"] = &fakeFIBHandle{publishErr: errors.New("boom")}
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.Error(t, err)

	require.True(t, backend.lastModuleHandle["cfg"].freed, "the built module must be discarded when nothing published")
	require.True(t, backend.lastFIBHandle["cfg"].freed, "the object that failed to publish must be discarded")
	require.Equal(t, []string{"free fib", "free module"}, backend.events,
		"neither handle ever published, but both must still be freed")

	list, err := service.ListConfigs(t.Context(), &routepb.ListConfigsRequest{})
	require.NoError(t, err)
	require.NotContains(t, list.GetConfigs(), "cfg", "nothing was published, so the config must not be tracked")
}

// A module publish failing after the first object went out keeps the
// object published and tracked, and DeleteConfig still tears it down.
func Test_RouteService_UpdateFIB_ModulePublishFailureAfterFirstObjectKeepsObjectPublished(t *testing.T) {
	backend := newFakeBackend()
	backend.nextModuleHandle["cfg"] = &fakeModuleHandle{publishErr: errors.New("boom")}
	service := route.NewRouteService(backend)

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.Error(t, err)

	module := backend.lastModuleHandle["cfg"]
	require.True(t, module.freed, "the module that failed to publish must be discarded immediately")
	require.Equal(t, []string{"publish fib", "free module"}, backend.events,
		"the object published and the module that failed to publish was discarded")

	list, err := service.ListConfigs(t.Context(), &routepb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Contains(t, list.GetConfigs(), "cfg", "the object that did publish must still be tracked")

	_, err = service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "cfg"})
	require.NoError(t, err)
}

// DeleteConfig deletes the module before the object and tolerates
// NotFound on either half.
func Test_RouteService_DeleteConfig_DeletesModuleBeforeObjectAndToleratesNotFound(t *testing.T) {
	tests := []struct {
		name         string
		removeModule bool
		removeObject bool
	}{
		{name: "both present"},
		{name: "module already gone", removeModule: true},
		{name: "object already gone", removeObject: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend()
			service := route.NewRouteService(backend)

			entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))
			_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
				ModuleName: "cfg",
				Entries:    []*routepb.FIBEntry{entry},
			})
			require.NoError(t, err)

			if test.removeModule {
				delete(backend.modulePublished, "cfg")
			}
			if test.removeObject {
				delete(backend.fibPublished, "cfg")
			}

			_, err = service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "cfg"})
			require.NoError(t, err)
			require.Equal(t,
				[]string{"module", "fib", "delete_module", "delete_fib"},
				backend.calls["cfg"],
				"the module must be deleted before the object even when one half is already gone",
			)
		})
	}
}

// A refused free parks the entry until ReclaimDeferred succeeds, and a
// module passed along is freed only by the last entry holding it.
func Test_RouteService_ReclaimDeferred_RetriesRefusedFreeUntilItSucceeds(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	// Both updates name the same device, so they share one module: its
	// own free count is what proves "freed only once the last entry
	// retires."
	entry1 := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", ""))
	_, err := service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry1},
	})
	require.NoError(t, err)
	firstObject := backend.lastFIBHandle["cfg"]
	module := backend.lastModuleHandle["cfg"]

	firstObject.freeRefusals = 2

	entry2 := testFIBEntry(t, "10.0.1.0/32", testNexthop("eth0", ""))
	_, err = service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry2},
	})
	require.NoError(t, err)
	require.False(t, firstObject.freed, "the refused free must park the entry rather than lose it")
	require.False(t, module.freed, "the module is still held by the entry now published")

	service.ReclaimDeferred()
	require.False(t, firstObject.freed, "one retry must still be refused")

	service.ReclaimDeferred()
	require.True(t, firstObject.freed, "the second retry must drain and free the parked entry")
	require.False(t, module.freed, "the module is still referenced by the live entry")

	_, err = service.DeleteConfig(t.Context(), &routepb.DeleteConfigRequest{Name: "cfg"})
	require.NoError(t, err)
	require.Equal(t, 1, module.freeCount, "the module must be freed exactly once, when the last entry retires")
}

// ShowFIB maps a missing config to NotFound and renders what the backend
// dumped.
func Test_RouteService_ShowFIB_MapsNotFoundAndRendersDumpedEntries(t *testing.T) {
	backend := newFakeBackend()
	service := route.NewRouteService(backend)

	_, err := service.ShowFIB(t.Context(), &routepb.ShowFIBRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))

	entry := testFIBEntry(t, "10.0.0.0/32", testNexthop("eth0", "nexthop_x"))
	_, err = service.UpdateFIB(t.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "cfg",
		Entries:    []*routepb.FIBEntry{entry},
	})
	require.NoError(t, err)

	addr := netip.MustParseAddr("10.0.0.0")
	backend.dumpEntries["cfg"] = []croute.FIBEntry{{
		AddressFamily: croute.AddressFamilyIPv4,
		PrefixFrom:    addr,
		PrefixTo:      addr,
		Nexthops: []croute.FIBNexthop{{
			DstMAC:  net.HardwareAddr(testDstMAC[:]),
			SrcMAC:  net.HardwareAddr(testSrcMAC[:]),
			Device:  "eth0",
			Counter: "nexthop_x",
		}},
	}}

	resp, err := service.ShowFIB(t.Context(), &routepb.ShowFIBRequest{Name: "cfg"})
	require.NoError(t, err)
	require.Len(t, resp.GetEntries(), 1)

	got := resp.GetEntries()[0]
	gotStart, gotEnd, err := got.GetRange().ToRange()
	require.NoError(t, err)
	require.Equal(t, addr, gotStart)
	require.Equal(t, addr, gotEnd)

	require.Len(t, got.GetNexthops(), 1)
	require.Equal(t, "eth0", got.GetNexthops()[0].GetDevice())
	require.Equal(t, "nexthop_x", got.GetNexthops()[0].GetCounter())
}
