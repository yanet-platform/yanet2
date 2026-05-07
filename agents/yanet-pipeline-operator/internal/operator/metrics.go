package operator

import (
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

// Resource kinds reported via OnResourceUpdated.
const (
	kindPipeline    = "pipeline"
	kindDevicePlain = "device-plain"
	kindDeviceVlan  = "device-vlan"
)

var reconcilerStateNames = map[ReconcilerState]string{
	ReconcilerStateIdle:     "idle",
	ReconcilerStateApplying: "applying",
	ReconcilerStateSleeping: "sleeping",
}

// Metrics is the single observability sink for the operator.
type Metrics struct {
	reconcileTotal  metrics.Counter
	reconcileErrors metrics.Counter
	stageAdvance    metrics.Counter

	states         map[ReconcilerState]*metrics.Gauge
	queueDepth     metrics.Gauge
	backoffSeconds metrics.Gauge

	gateways []*GatewayMetrics
}

func NewMetrics(gateways []*GatewayMetrics) *Metrics {
	states := make(map[ReconcilerState]*metrics.Gauge, len(reconcilerStateNames))
	for state := range reconcilerStateNames {
		states[state] = &metrics.Gauge{}
	}

	return &Metrics{
		states:   states,
		gateways: gateways,
	}
}

func (m *Metrics) OnReconcileCompleted(err error) {
	m.reconcileTotal.Inc()
	if err != nil {
		m.reconcileErrors.Inc()
	}
}

func (m *Metrics) OnStageAdvanced() {
	m.stageAdvance.Inc()
}

func (m *Metrics) OnBackoffScheduled(delay time.Duration) {
	m.backoffSeconds.Store(delay.Seconds())
}

func (m *Metrics) OnBackoffReset() {
	m.backoffSeconds.Store(0)
}

func (m *Metrics) OnQueueChanged(depth int) {
	m.queueDepth.Store(float64(depth))
}

func (m *Metrics) OnStateChanged(state ReconcilerState) {
	for k, g := range m.states {
		if k == state {
			g.Store(1)
		} else {
			g.Store(0)
		}
	}
}

// Collect renders the current state of every metric as a []*commonpb.Metric.
func (m *Metrics) Collect() []*commonpb.Metric {
	out := make([]*commonpb.Metric, 0)

	out = append(out, makeCounter(
		"pipeline_operator_reconcile_total",
		m.reconcileTotal.Load(),
	))
	out = append(out, makeCounter(
		"pipeline_operator_reconcile_errors_total",
		m.reconcileErrors.Load(),
	))
	out = append(out, makeCounter(
		"pipeline_operator_stage_advance_total",
		m.stageAdvance.Load(),
	))

	for state, g := range m.states {
		out = append(out, makeGauge(
			"pipeline_operator_state",
			g.Load(),
			makeLabel("state", reconcilerStateNames[state]),
		))
	}
	out = append(out, makeGauge(
		"pipeline_operator_queue_depth",
		m.queueDepth.Load(),
	))
	out = append(out, makeGauge(
		"pipeline_operator_backoff_seconds",
		m.backoffSeconds.Load(),
	))

	for _, g := range m.gateways {
		out = append(out, g.collect()...)
	}

	return out
}

// GatewayMetrics is the per-gateway implementation of
// GatewayActuatorMetricsObserver.
type GatewayMetrics struct {
	name string

	resourceUpdate       map[string]*metrics.Counter
	resourceUpdateErrors map[string]*metrics.Counter

	apply       metrics.Counter
	applyErrors metrics.Counter

	gcRuns         metrics.Counter
	gcErrors       metrics.Counter
	gcDeleted      metrics.Counter
	gcDeleteErrors metrics.Counter
}

func NewGatewayMetrics(name string) *GatewayMetrics {
	kinds := []string{kindPipeline, kindDevicePlain, kindDeviceVlan}

	resourceUpdate := make(map[string]*metrics.Counter, len(kinds))
	resourceUpdateErrors := make(map[string]*metrics.Counter, len(kinds))
	for _, k := range kinds {
		resourceUpdate[k] = &metrics.Counter{}
		resourceUpdateErrors[k] = &metrics.Counter{}
	}

	return &GatewayMetrics{
		name:                 name,
		resourceUpdate:       resourceUpdate,
		resourceUpdateErrors: resourceUpdateErrors,
	}
}

func (m *GatewayMetrics) OnApplyCompleted(err error) {
	m.apply.Inc()
	if err != nil {
		m.applyErrors.Inc()
	}
}

func (m *GatewayMetrics) OnResourceUpdated(kind string, err error) {
	if c, ok := m.resourceUpdate[kind]; ok {
		c.Inc()
	}

	if err != nil {
		if c, ok := m.resourceUpdateErrors[kind]; ok {
			c.Inc()
		}
	}
}

// OnGC records the outcome of one garbage-collection pass.
func (m *GatewayMetrics) OnGC(deleted, failed int, err error) {
	m.gcRuns.Inc()
	if err != nil || failed > 0 {
		m.gcErrors.Inc()
	}
	if deleted > 0 {
		m.gcDeleted.Add(uint64(deleted))
	}
	if failed > 0 {
		m.gcDeleteErrors.Add(uint64(failed))
	}
}

func (m *GatewayMetrics) collect() []*commonpb.Metric {
	gw := makeLabel("gateway", m.name)
	out := make([]*commonpb.Metric, 0)

	out = append(out, makeCounter(
		"pipeline_operator_gateway_apply_total",
		m.apply.Load(),
		gw,
	))
	out = append(out, makeCounter(
		"pipeline_operator_gateway_apply_errors_total",
		m.applyErrors.Load(),
		gw,
	))

	for kind, c := range m.resourceUpdate {
		out = append(out, makeCounter(
			"pipeline_operator_resource_update_total",
			c.Load(),
			gw, makeLabel("kind", kind),
		))
	}
	for kind, c := range m.resourceUpdateErrors {
		out = append(out, makeCounter(
			"pipeline_operator_resource_update_errors_total",
			c.Load(),
			gw, makeLabel("kind", kind),
		))
	}

	out = append(out,
		makeCounter("pipeline_operator_gc_runs_total", m.gcRuns.Load(), gw),
		makeCounter("pipeline_operator_gc_errors_total", m.gcErrors.Load(), gw),
		makeCounter(
			"pipeline_operator_gc_pipelines_deleted_total",
			m.gcDeleted.Load(),
			gw,
		),
		makeCounter(
			"pipeline_operator_gc_pipelines_delete_errors_total",
			m.gcDeleteErrors.Load(),
			gw,
		),
	)
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
