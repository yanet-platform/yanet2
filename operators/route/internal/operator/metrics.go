package operator

import (
	"sync"
	"time"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
)

// applyDurationBounds are the histogram bucket upper bounds, in seconds,
// used for per-gateway apply latency.
var applyDurationBounds = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

var reconcilerStateNames = map[operator.ReconcilerState]string{
	operator.ReconcilerStateIdle:     "idle",
	operator.ReconcilerStateApplying: "applying",
	operator.ReconcilerStateSleeping: "sleeping",
}

// Metrics is the single observability sink for the route operator.
//
// It combines three collection styles: reconcile-loop and gateway-apply
// counters are updated by tee-ing existing callbacks, RIB gauges are
// pulled from thread-safe snapshots at Collect time, and gateway sinks
// are created on demand by Gateway.
type Metrics struct {
	ribs                  *RIBStore
	neighTable            *neigh.NeighTable
	netlinkMonitorEnabled bool

	reconcileTotal  metrics.Counter
	reconcileErrors metrics.Counter
	states          map[operator.ReconcilerState]*metrics.Gauge
	backoffSeconds  metrics.Gauge

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

	states := make(map[operator.ReconcilerState]*metrics.Gauge, len(reconcilerStateNames))
	for state := range reconcilerStateNames {
		states[state] = &metrics.Gauge{}
	}

	return &Metrics{
		ribs:                  ribs,
		neighTable:            neighTable,
		netlinkMonitorEnabled: opts.NetlinkMonitorEnabled,
		states:                states,
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

// OnReconcileCompleted records the outcome of one reconcile pass.
func (m *Metrics) OnReconcileCompleted(err error) {
	m.reconcileTotal.Inc()
	if err != nil {
		m.reconcileErrors.Inc()
	}
}

// OnBackoffScheduled records the delay before the next retry.
func (m *Metrics) OnBackoffScheduled(delay time.Duration) {
	m.backoffSeconds.Store(delay.Seconds())
}

// OnBackoffReset clears the backoff gauge after a successful apply.
func (m *Metrics) OnBackoffReset() {
	m.backoffSeconds.Store(0)
}

// OnStateChanged records the reconcile loop's current state.
func (m *Metrics) OnStateChanged(state operator.ReconcilerState) {
	for k, g := range m.states {
		if k == state {
			g.Store(1)
		} else {
			g.Store(0)
		}
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
// seeding also change RIB contents but are not counted here; they are
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
	out := make([]*commonpb.Metric, 0)

	out = append(out,
		makeCounter("route_operator_reconcile_total", m.reconcileTotal.Load()),
		makeCounter("route_operator_reconcile_errors_total", m.reconcileErrors.Load()),
		makeGauge("route_operator_backoff_seconds", m.backoffSeconds.Load()),
	)
	for state, g := range m.states {
		out = append(out, makeGauge(
			"route_operator_state",
			g.Load(),
			makeLabel("state", reconcilerStateNames[state]),
		))
	}

	for name, ribRef := range m.ribs.Snapshot() {
		stats := ribRef.Stats()
		module := makeLabel("module", name)
		out = append(out,
			makeGauge("route_operator_rib_prefixes", float64(stats.Prefixes), module),
			makeGauge("route_operator_rib_routes", float64(stats.Routes), module),
			makeGauge(
				"route_operator_rib_last_change_timestamp_seconds",
				float64(stats.ChangedAt.Unix()),
				module,
			),
		)
	}

	for _, src := range m.neighTable.ListSources() {
		out = append(out, makeGauge(
			"route_operator_neighbour_entries",
			float64(src.EntryCount),
			makeLabel("table", src.Name),
		))
	}
	_, nexthops := m.neighTable.View().Entries()
	out = append(out, makeGauge("route_operator_neighbour_nexthops", float64(nexthops)))

	for _, entry := range m.ribSessionStarts.Metrics() {
		out = append(out, makeCounter(
			"route_operator_rib_session_starts_total",
			entry.Value.Load(),
			makeLabel("module", entry.ID.Labels["module"]),
		))
	}
	for _, entry := range m.ribSessionEnds.Metrics() {
		out = append(out, makeCounter(
			"route_operator_rib_session_ends_total",
			entry.Value.Load(),
			makeLabel("module", entry.ID.Labels["module"]),
		))
	}
	out = append(out, makeCounter("route_operator_rib_feed_updates_total", m.ribFeedUpdates.Load()))

	if m.netlinkMonitorEnabled {
		out = append(out,
			makeGauge("route_operator_neighbour_monitor_healthy", m.neighbourHealthy.Load()),
			makeCounter("route_operator_neighbour_resyncs_total", m.neighbourResyncs.Load()),
			makeCounter("route_operator_neighbour_syncs_total", m.neighbourSyncs.Load()),
		)
	}

	m.gatewaysMu.Lock()
	gateways := make([]*GatewayMetrics, 0, len(m.gateways))
	for _, g := range m.gateways {
		gateways = append(gateways, g)
	}
	m.gatewaysMu.Unlock()

	for _, g := range gateways {
		out = append(out, g.collect()...)
	}

	return out
}

// fibGauges holds the per-module FIB build gauges reported by a single
// gateway.
type fibGauges struct {
	entries            metrics.Gauge
	routes             metrics.Gauge
	unresolvedNexthops metrics.Gauge
	skippedPrefixes    metrics.Gauge
	filteredRoutes     metrics.Gauge
}

// GatewayMetrics is the per-gateway observability sink, fed by
// meteredActuator (apply outcomes) and GatewayActuator (FIB build
// stats).
type GatewayMetrics struct {
	name string

	apply         metrics.Counter
	applyErrors   metrics.Counter
	applyDuration *metrics.Histogram

	fibMu sync.Mutex
	fib   map[string]*fibGauges
}

// newGatewayMetrics constructs the metrics sink for a single gateway.
func newGatewayMetrics(name string) *GatewayMetrics {
	return &GatewayMetrics{
		name:          name,
		applyDuration: metrics.NewHistogram(applyDurationBounds),
		fib:           map[string]*fibGauges{},
	}
}

// ObserveApply records the outcome and duration of one Apply call.
func (m *GatewayMetrics) ObserveApply(d time.Duration, err error) {
	m.apply.Inc()
	if err != nil {
		m.applyErrors.Inc()
	}
	m.applyDuration.Observe(d.Seconds())
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

	g.entries.Store(float64(stats.PrefixesAdded))
	g.routes.Store(float64(stats.TotalRoutes))
	g.unresolvedNexthops.Store(float64(stats.NeighbourNotFound))
	g.skippedPrefixes.Store(float64(stats.SkippedPrefixes))
	g.filteredRoutes.Store(float64(stats.FilteredRoutes))
}

// collect renders this gateway's metrics as a slice of commonpb.Metric
// values.
func (m *GatewayMetrics) collect() []*commonpb.Metric {
	gateway := makeLabel("gateway", m.name)
	out := make([]*commonpb.Metric, 0)

	out = append(out,
		makeCounter("route_operator_gateway_apply_total", m.apply.Load(), gateway),
		makeCounter("route_operator_gateway_apply_errors_total", m.applyErrors.Load(), gateway),
		makeHistogram("route_operator_gateway_apply_duration_seconds", m.applyDuration, gateway),
	)

	m.fibMu.Lock()
	defer m.fibMu.Unlock()

	for module, g := range m.fib {
		labels := []*commonpb.Label{gateway, makeLabel("module", module)}
		out = append(out,
			makeGauge("route_operator_fib_entries", g.entries.Load(), labels...),
			makeGauge("route_operator_fib_routes", g.routes.Load(), labels...),
			makeGauge("route_operator_fib_unresolved_nexthops", g.unresolvedNexthops.Load(), labels...),
			makeGauge("route_operator_fib_skipped_prefixes", g.skippedPrefixes.Load(), labels...),
			makeGauge("route_operator_fib_filtered_routes", g.filteredRoutes.Load(), labels...),
		)
	}

	return out
}

func makeLabel(name, value string) *commonpb.Label {
	return &commonpb.Label{Name: name, Value: value}
}

func makeCounter(name string, value uint64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Counter{Counter: value},
	}
}

func makeGauge(name string, value float64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Gauge{Gauge: value},
	}
}

func makeHistogram(name string, h *metrics.Histogram, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  commonpb.MetricValueToProto(h),
	}
}
