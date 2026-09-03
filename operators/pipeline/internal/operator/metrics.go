package operator

import (
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

// Resource kinds reported via OnResourceUpdated.
const (
	kindPipeline    = "pipeline"
	kindDevicePlain = "device-plain"
	kindDeviceVlan  = "device-vlan"
)

// Metrics is the single observability sink for the operator.
type Metrics struct {
	reconcilerMetrics *operator.ReconcilerMetrics

	stageAdvance metrics.Counter
	queueDepth   metrics.Gauge

	gateways []*GatewayMetrics
}

// NewMetrics combines shared reconcile metrics with pipeline-specific metrics.
//
// The supplied reconcile collector is also wired directly into the generic
// reconciler, while this value provides the complete service snapshot.
func NewMetrics(
	reconcilerMetrics *operator.ReconcilerMetrics,
	gateways []*GatewayMetrics,
) *Metrics {
	return &Metrics{
		reconcilerMetrics: reconcilerMetrics,
		gateways:          gateways,
	}
}

func (m *Metrics) OnStageAdvanced() {
	m.stageAdvance.Inc()
}

func (m *Metrics) OnQueueChanged(depth int) {
	m.queueDepth.Store(float64(depth))
}

func (m *Metrics) Collect() []*commonpb.Metric {
	out := m.reconcilerMetrics.Collect()

	out = append(out, commonpb.NewMetricCounter(
		"pipeline_operator_stage_advance_total",
		m.stageAdvance.Load(),
	))
	out = append(out, commonpb.NewMetricGauge(
		"pipeline_operator_queue_depth",
		m.queueDepth.Load(),
	))

	for _, g := range m.gateways {
		out = append(out, g.Collect()...)
	}

	return out
}

// GatewayMetrics is the per-gateway implementation of
// GatewayActuatorMetricsObserver.
type GatewayMetrics struct {
	applyMetrics *operator.ApplyMetrics
	name         string

	resourceUpdate       map[string]*metrics.Counter
	resourceUpdateErrors map[string]*metrics.Counter

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
		applyMetrics: operator.NewApplyMetrics(
			"pipeline_operator_gateway",
			commonpb.NewLabel("gateway", name),
		),
		name:                 name,
		resourceUpdate:       resourceUpdate,
		resourceUpdateErrors: resourceUpdateErrors,
	}
}

func (m *GatewayMetrics) OnApplyCompleted(err error) {
	m.applyMetrics.Observe(err)
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

func (m *GatewayMetrics) Collect() []*commonpb.Metric {
	gw := commonpb.NewLabel("gateway", m.name)
	out := m.applyMetrics.Collect()

	for kind, c := range m.resourceUpdate {
		out = append(out, commonpb.NewMetricCounter(
			"pipeline_operator_resource_update_total",
			c.Load(),
			gw, commonpb.NewLabel("kind", kind),
		))
	}
	for kind, c := range m.resourceUpdateErrors {
		out = append(out, commonpb.NewMetricCounter(
			"pipeline_operator_resource_update_errors_total",
			c.Load(),
			gw, commonpb.NewLabel("kind", kind),
		))
	}

	out = append(out,
		commonpb.NewMetricCounter("pipeline_operator_gc_runs_total", m.gcRuns.Load(), gw),
		commonpb.NewMetricCounter("pipeline_operator_gc_errors_total", m.gcErrors.Load(), gw),
		commonpb.NewMetricCounter(
			"pipeline_operator_gc_pipelines_deleted_total",
			m.gcDeleted.Load(),
			gw,
		),
		commonpb.NewMetricCounter(
			"pipeline_operator_gc_pipelines_delete_errors_total",
			m.gcDeleteErrors.Load(),
			gw,
		),
	)
	return out
}
