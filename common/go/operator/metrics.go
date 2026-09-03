package operator

import (
	"context"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

var reconcilerMetricStates = []ReconcilerState{
	ReconcilerStateIdle,
	ReconcilerStateApplying,
	ReconcilerStateSleeping,
}

const reconcilerMetricStateUnknown ReconcilerState = -1

// MetricsCollector exposes operator metrics to a metrics service.
type MetricsCollector interface {
	Collect() []*commonpb.Metric
}

func makeMetricName(prefix, suffix string) string {
	if prefix == "" {
		return suffix
	}
	return prefix + "_" + suffix
}

// ReconcilerMetrics records the standard reconcile lifecycle metrics.
type ReconcilerMetrics struct {
	prefix string
	labels []*commonpb.Label

	total  metrics.Counter
	errors metrics.Counter

	backoffSeconds metrics.Gauge
	state          atomic.Int32
}

// ApplyMetrics records the outcomes of applying state to one target.
type ApplyMetrics struct {
	prefix string
	labels []*commonpb.Label

	total  metrics.Counter
	errors metrics.Counter
}

// NewReconcilerMetrics constructs a collector and observer for the standard
// reconcile lifecycle metric families.
func NewReconcilerMetrics(
	prefix string,
	labels ...*commonpb.Label,
) *ReconcilerMetrics {
	collector := &ReconcilerMetrics{
		prefix: prefix,
		labels: labels,
	}
	collector.OnStateChanged(reconcilerMetricStateUnknown)
	return collector
}

// OnReconcileCompleted records one reconcile attempt and whether it failed.
func (m *ReconcilerMetrics) OnReconcileCompleted(err error) {
	m.total.Inc()
	if err != nil {
		m.errors.Inc()
	}
}

// OnBackoffScheduled records the delay before the next reconcile attempt.
func (m *ReconcilerMetrics) OnBackoffScheduled(delay time.Duration) {
	m.backoffSeconds.Store(delay.Seconds())
}

// OnBackoffReset clears the retry delay after reconciliation recovers.
func (m *ReconcilerMetrics) OnBackoffReset() {
	m.backoffSeconds.Store(0)
}

// OnStateChanged records the reconcile loop's current lifecycle state.
func (m *ReconcilerMetrics) OnStateChanged(state ReconcilerState) {
	m.state.Store(int32(state))
}

func (m *ReconcilerMetrics) Collect() []*commonpb.Metric {
	// Load errors first because updates publish the attempt first. This keeps
	// a concurrent snapshot from reporting more errors than attempts.
	errors := m.errors.Load()
	total := m.total.Load()
	metricList := []*commonpb.Metric{
		commonpb.NewMetricCounter(
			makeMetricName(m.prefix, "reconcile_total"),
			total,
			m.labels...,
		),
		commonpb.NewMetricCounter(
			makeMetricName(m.prefix, "reconcile_errors_total"),
			errors,
			m.labels...,
		),
		commonpb.NewMetricGauge(
			makeMetricName(m.prefix, "backoff_seconds"),
			m.backoffSeconds.Load(),
			m.labels...,
		),
	}

	currentState := ReconcilerState(m.state.Load())
	for _, state := range reconcilerMetricStates {
		value := 0.0
		if state == currentState {
			value = 1
		}
		labels := make([]*commonpb.Label, 0, len(m.labels)+1)
		labels = append(labels, m.labels...)
		labels = append(labels, commonpb.NewLabel("state", reconcilerStateName(state)))
		metricList = append(metricList, commonpb.NewMetricGauge(
			makeMetricName(m.prefix, "state"),
			value,
			labels...,
		))
	}

	return metricList
}

// NewApplyMetrics constructs a collector for apply outcomes.
func NewApplyMetrics(prefix string, labels ...*commonpb.Label) *ApplyMetrics {
	return &ApplyMetrics{
		prefix: prefix,
		labels: labels,
	}
}

// Observe records one apply attempt and whether it failed.
func (m *ApplyMetrics) Observe(err error) {
	m.total.Inc()
	if err != nil {
		m.errors.Inc()
	}
}

func (m *ApplyMetrics) Collect() []*commonpb.Metric {
	// Load errors first because updates publish the attempt first. This keeps
	// a concurrent snapshot from reporting more errors than attempts.
	errors := m.errors.Load()
	total := m.total.Load()
	return []*commonpb.Metric{
		commonpb.NewMetricCounter(
			makeMetricName(m.prefix, "apply_total"),
			total,
			m.labels...,
		),
		commonpb.NewMetricCounter(
			makeMetricName(m.prefix, "apply_errors_total"),
			errors,
			m.labels...,
		),
	}
}

func reconcilerStateName(state ReconcilerState) string {
	switch state {
	case ReconcilerStateIdle:
		return "idle"
	case ReconcilerStateApplying:
		return "applying"
	case ReconcilerStateSleeping:
		return "sleeping"
	default:
		return "unknown"
	}
}

type metricsService struct {
	ynpb.UnimplementedMetricsServiceServer

	collectors []MetricsCollector
}

// GetMetrics returns the operator metrics matching every requested tag.
func (m *metricsService) GetMetrics(
	ctx context.Context,
	req *commonpb.GetMetricsRequest,
) (*commonpb.GetMetricsResponse, error) {
	metricList := []*commonpb.Metric{}
	for _, collector := range m.collectors {
		metricList = append(metricList, collector.Collect()...)
	}

	return &commonpb.GetMetricsResponse{
		Metrics: metrics.Filter(metricList, req.GetTags()),
	}, nil
}

// NewMetricsServiceRegistrar returns a registrar for the shared metrics
// contract under an operator instance name.
func NewMetricsServiceRegistrar(name string, collectors ...MetricsCollector) ServiceRegistrar {
	return func(server *grpc.Server) string {
		desc := ynpb.MetricsService_ServiceDesc
		desc.ServiceName = MetricsServiceName(name)
		server.RegisterService(&desc, &metricsService{collectors: collectors})
		return desc.ServiceName
	}
}

// MetricsServiceName returns the gRPC service name under which the named
// operator reports metrics through a gateway.
func MetricsServiceName(name string) string {
	return "operators." + name + ".operatorpb.v1.MetricsService"
}
