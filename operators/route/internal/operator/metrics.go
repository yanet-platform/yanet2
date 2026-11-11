package operator

import (
	"sync"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// Metrics is the route operator's own observability sink; the reconcile
// and gateway apply families come from the operator framework.
//
// RIB gauges are pulled from thread-safe snapshots on every collection,
// feed and neighbour events are counted from their callbacks, and the FIB
// gauges of each gateway are created on its first build.
type Metrics struct {
	ribs                  *RIBStore
	neighTable            *neigh.NeighTable
	netlinkMonitorEnabled bool

	ribSessionStarts *metrics.MetricMap[*metrics.Counter]
	ribSessionEnds   *metrics.MetricMap[*metrics.Counter]
	ribFeedUpdates   metrics.Counter

	neighbourHealthy metrics.Gauge
	neighbourResyncs metrics.Counter
	neighbourSyncs   metrics.Counter

	gatewaysMu sync.Mutex
	gateways   map[string]*GatewayMetrics
}

// NewMetrics constructs the Metrics sink from the operator-owned
// dependencies it pulls from at Collect time.
func NewMetrics(ribs *RIBStore, neighTable *neigh.NeighTable, options ...MetricsOption) *Metrics {
	opts := newMetricsOptions()
	for _, o := range options {
		o(opts)
	}

	return &Metrics{
		ribs:                  ribs,
		neighTable:            neighTable,
		netlinkMonitorEnabled: opts.NetlinkMonitorEnabled,
		ribSessionStarts:      metrics.NewMetricMap[*metrics.Counter](),
		ribSessionEnds:        metrics.NewMetricMap[*metrics.Counter](),
		gateways:              map[string]*GatewayMetrics{},
	}
}

// MetricsFactory constructs the operator metrics sink from the
// operator-owned dependencies.
//
// NewOperator invokes the factory once the RIB store and neighbour table
// exist, so a custom factory can wrap or extend the default sink while
// still receiving the real pull sources.
type MetricsFactory func(ribs *RIBStore, neighTable *neigh.NeighTable, options ...MetricsOption) *Metrics

// metricsOptions holds the configurable toggles for NewMetrics.
type metricsOptions struct {
	NetlinkMonitorEnabled bool
}

func newMetricsOptions() *metricsOptions {
	return &metricsOptions{}
}

// MetricsOption configures NewMetrics.
type MetricsOption func(*metricsOptions)

// WithNetlinkMonitorMetrics enables the netlink-monitor health metric
// family.
//
// This covers route_operator_neighbour_monitor_healthy,
// route_operator_neighbour_resyncs_total, and
// route_operator_neighbour_syncs_total. Pass this option only when the
// netlink monitor actually runs, so a deliberately disabled monitor does
// not read as unhealthy.
func WithNetlinkMonitorMetrics() MetricsOption {
	return func(o *metricsOptions) {
		o.NetlinkMonitorEnabled = true
	}
}

// OnRIBSessionStart records that a FeedRIB stream session began for the
// named module config.
func (m *Metrics) OnRIBSessionStart(name string, sessionID uint64) {
	m.ribSessionCounter(m.ribSessionStarts, name).Inc()
}

// OnRIBSessionEnd records that a FeedRIB stream session ended for the
// named module config.
func (m *Metrics) OnRIBSessionEnd(name string, sessionID uint64) {
	m.ribSessionCounter(m.ribSessionEnds, name).Inc()
}

// OnRIBUpdate records that a batch of n routes was applied to a RIB
// through a FeedRIB stream session.
//
// Unary route mutations (InsertRoute/DeleteRoute) and static-config
// seeding also change RIB contents but are not counted here — they are
// visible through the RIB gauges instead.
func (m *Metrics) OnRIBUpdate(n int) {
	m.ribFeedUpdates.Add(uint64(n))
}

// OnNeighbourSynced records the transition to a healthy neighbour table
// after the initial sync.
func (m *Metrics) OnNeighbourSynced() {
	m.neighbourHealthy.Store(1)
}

// OnNeighbourError records a degradation episode in the neighbour
// monitor.
func (m *Metrics) OnNeighbourError(err error) {
	m.neighbourHealthy.Store(0)
}

// OnNeighbourResynced records recovery from a degradation episode.
func (m *Metrics) OnNeighbourResynced() {
	m.neighbourHealthy.Store(1)
	m.neighbourResyncs.Inc()
}

// OnNeighbourHealthy records one successful neighbour table refresh.
func (m *Metrics) OnNeighbourHealthy() {
	m.neighbourSyncs.Inc()
}

// Gateway returns the per-gateway metrics for name, creating them on
// first use.
func (m *Metrics) Gateway(name string) *GatewayMetrics {
	m.gatewaysMu.Lock()
	defer m.gatewaysMu.Unlock()

	if g, ok := m.gateways[name]; ok {
		return g
	}

	g := newGatewayMetrics(name)
	m.gateways[name] = g
	return g
}

// ribSessionCounter returns the per-module counter from the given map,
// creating it on first use.
func (m *Metrics) ribSessionCounter(sessions *metrics.MetricMap[*metrics.Counter], module string) *metrics.Counter {
	id := metrics.MetricID{Labels: metrics.Labels{"module": module}}
	return sessions.GetOrCreate(id, func() *metrics.Counter { return &metrics.Counter{} })
}

// Collect renders the current state of every metric as a slice of
// commonpb.Metric values.
func (m *Metrics) Collect() []*commonpb.Metric {
	out := []*commonpb.Metric{}

	for name, ribRef := range m.ribs.Snapshot() {
		stats := ribRef.Stats()
		module := commonpb.NewLabel("module", name)
		out = append(out,
			commonpb.NewMetricGauge("route_operator_rib_prefixes", float64(stats.Prefixes), module),
			commonpb.NewMetricGauge("route_operator_rib_routes", float64(stats.Routes), module),
			commonpb.NewMetricGauge(
				"route_operator_rib_last_change_timestamp_seconds",
				float64(stats.ChangedAt.Unix()),
				module,
			),
		)
	}

	for _, src := range m.neighTable.ListSources() {
		out = append(out, commonpb.NewMetricGauge(
			"route_operator_neighbour_entries",
			float64(src.EntryCount),
			commonpb.NewLabel("table", src.Name),
		))
	}
	nexthops := m.neighTable.View().Len()
	out = append(out, commonpb.NewMetricGauge("route_operator_neighbour_nexthops", float64(nexthops)))

	for _, entry := range m.ribSessionStarts.Metrics() {
		out = append(out, commonpb.NewMetricCounter(
			"route_operator_rib_session_starts_total",
			entry.Value.Load(),
			commonpb.NewLabel("module", entry.ID.Labels["module"]),
		))
	}
	for _, entry := range m.ribSessionEnds.Metrics() {
		out = append(out, commonpb.NewMetricCounter(
			"route_operator_rib_session_ends_total",
			entry.Value.Load(),
			commonpb.NewLabel("module", entry.ID.Labels["module"]),
		))
	}
	out = append(out, commonpb.NewMetricCounter("route_operator_rib_feed_updates_total", m.ribFeedUpdates.Load()))

	if m.netlinkMonitorEnabled {
		out = append(out,
			commonpb.NewMetricGauge("route_operator_neighbour_monitor_healthy", m.neighbourHealthy.Load()),
			commonpb.NewMetricCounter("route_operator_neighbour_resyncs_total", m.neighbourResyncs.Load()),
			commonpb.NewMetricCounter("route_operator_neighbour_syncs_total", m.neighbourSyncs.Load()),
		)
	}

	m.gatewaysMu.Lock()
	gateways := make([]*GatewayMetrics, 0, len(m.gateways))
	for _, g := range m.gateways {
		gateways = append(gateways, g)
	}
	m.gatewaysMu.Unlock()

	for _, g := range gateways {
		out = append(out, g.Collect()...)
	}

	return out
}

// fibGauges holds the per-module FIB build gauges reported by a single
// gateway.
type fibGauges struct {
	// Entries is the number of FIB prefixes added by the last build.
	Entries metrics.Gauge
	// Routes is the total number of routes seen by the last build.
	Routes metrics.Gauge
	// UnresolvedNexthops counts route records whose neighbours were missing
	// in the last build's full snapshot; device ownership is unknown.
	//
	// Each gateway counts these records independently, so summing gateways
	// does not give a unique-neighbour loss count.
	UnresolvedNexthops metrics.Gauge
	// DeviceExcludedRoutes counts resolved route records excluded by the
	// gateway's device restriction in the last build.
	DeviceExcludedRoutes metrics.Gauge
	// SkippedPrefixes is the number of prefixes with no routes at all in
	// the last build.
	SkippedPrefixes metrics.Gauge
	// FilteredRoutes is the number of eligible routes dropped because a
	// better route of the same source exists.
	FilteredRoutes metrics.Gauge
}

// Store records the outcome of one FIB build as gauge values.
func (m *fibGauges) Store(stats FIBBuildStats) {
	m.Entries.Store(float64(stats.PrefixesAdded))
	m.Routes.Store(float64(stats.TotalRoutes))
	m.UnresolvedNexthops.Store(float64(stats.NeighbourNotFound))
	m.DeviceExcludedRoutes.Store(float64(stats.DeviceExcludedRoutes))
	m.SkippedPrefixes.Store(float64(stats.SkippedPrefixes))
	m.FilteredRoutes.Store(float64(stats.FilteredRoutes))
}

// GatewayMetrics holds the FIB build gauges of one gateway.
type GatewayMetrics struct {
	name string

	fibMu sync.Mutex
	fib   map[string]*fibGauges
}

// newGatewayMetrics constructs the metrics sink for a single gateway.
func newGatewayMetrics(name string) *GatewayMetrics {
	return &GatewayMetrics{
		name: name,
		fib:  map[string]*fibGauges{},
	}
}

// OnFIBBuilt records the outcome of one FIB build for module.
func (m *GatewayMetrics) OnFIBBuilt(module string, stats FIBBuildStats) {
	m.fibMu.Lock()
	g, ok := m.fib[module]
	if !ok {
		g = &fibGauges{}
		m.fib[module] = g
	}
	m.fibMu.Unlock()

	g.Store(stats)
}

// Collect renders this gateway's metrics as a slice of commonpb.Metric
// values.
func (m *GatewayMetrics) Collect() []*commonpb.Metric {
	gateway := commonpb.NewLabel("gateway", m.name)
	out := make([]*commonpb.Metric, 0)

	m.fibMu.Lock()
	defer m.fibMu.Unlock()

	for module, g := range m.fib {
		labels := []*commonpb.Label{gateway, commonpb.NewLabel("module", module)}
		out = append(out,
			commonpb.NewMetricGauge("route_operator_fib_entries", g.Entries.Load(), labels...),
			commonpb.NewMetricGauge("route_operator_fib_routes", g.Routes.Load(), labels...),
			commonpb.NewMetricGauge("route_operator_fib_unresolved_nexthops", g.UnresolvedNexthops.Load(), labels...),
			commonpb.NewMetricGauge("route_operator_fib_device_excluded_routes", g.DeviceExcludedRoutes.Load(), labels...),
			commonpb.NewMetricGauge("route_operator_fib_skipped_prefixes", g.SkippedPrefixes.Load(), labels...),
			commonpb.NewMetricGauge("route_operator_fib_filtered_routes", g.FilteredRoutes.Load(), labels...),
		)
	}

	return out
}
