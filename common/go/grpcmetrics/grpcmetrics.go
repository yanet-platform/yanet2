// Package grpcmetrics provides an opt-in gRPC unary server interceptor that
// records per-call metrics and renders them to commonpb.Metric.
//
// Each ServerMetrics instance tracks three metric families:
//   - grpc_server_started_total    — counter per {grpc_type, grpc_service, grpc_method[, config]}
//   - grpc_server_handled_total    — counter per {grpc_type, grpc_service, grpc_method, grpc_code[, config]}
//   - grpc_server_handling_seconds — histogram per {grpc_type, grpc_service, grpc_method[, config]}
//
// Label names follow the go-grpc-prometheus (go-grpc-middleware/providers/prometheus)
// server convention. grpc_type is always "unary"; streaming is a documented
// future follow-up. The optional "config" label is emitted when the
// Labeler provides it.
//
// Stale series are pruned via a retention predicate.
package grpcmetrics

import (
	"context"
	"maps"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

const (
	labelType    = "grpc_type"
	labelService = "grpc_service"
	labelMethod  = "grpc_method"
	labelCode    = "grpc_code"
)

const grpcTypeUnary = "unary"

// DefaultBuckets is the default histogram bucket boundary set in seconds.
//
// The ladder covers sub-millisecond to multi-second latencies and matches the
// boundaries the ACL module used before migration to this library.
var DefaultBuckets = []float64{
	0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2,
	0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1, 1.5, 2, 3, 4, 5,
}

// Labeler derives extra per-call labels from a unary request.
//
// It is called once per RPC before the handler runs. A nil return means no
// extra labels are attached.
type Labeler func(fullMethod string, req any) metrics.Labels

// Retention reports which series to keep when pruning stale metrics.
//
// It is invoked once per Collect, OUTSIDE the metric maps' lock, and returns a
// pure predicate that the collector then applies to each series under the
// lock.
//
// Snapshot any external state (e.g. the set of live configs) in the outer
// function so the returned predicate takes no locks of its own.
type Retention func() func(metrics.MetricID) bool

type callSeries struct {
	started  metrics.Counter
	handling *metrics.Histogram
}

// newCallSeries creates a callSeries with a latency histogram over buckets.
func newCallSeries(buckets []float64) *callSeries {
	return &callSeries{handling: metrics.NewHistogram(buckets)}
}

// AppendTo appends this series' metrics, labelled with labels, to out.
func (m *callSeries) AppendTo(out []*commonpb.Metric, labels []*commonpb.Label) []*commonpb.Metric {
	return append(out,
		&commonpb.Metric{
			Name:   "grpc_server_started_total",
			Labels: labels,
			Value:  commonpb.MetricValueToProto(&m.started),
		},
		&commonpb.Metric{
			Name:   "grpc_server_handling_seconds",
			Labels: labels,
			Value:  commonpb.MetricValueToProto(m.handling),
		},
	)
}

// RecordStart counts a started call.
func (m *callSeries) RecordStart() {
	m.started.Inc()
}

// RecordFinish observes the handling latency in seconds.
func (m *callSeries) RecordFinish(seconds float64) {
	m.handling.Observe(seconds)
}

type codeSeries struct {
	count metrics.Counter
}

// AppendTo appends this series' metric, labelled with labels, to out.
func (m *codeSeries) AppendTo(out []*commonpb.Metric, labels []*commonpb.Label) []*commonpb.Metric {
	return append(out, &commonpb.Metric{
		Name:   "grpc_server_handled_total",
		Labels: labels,
		Value:  commonpb.MetricValueToProto(&m.count),
	})
}

// Record counts a handled call (by status code).
func (m *codeSeries) Record() {
	m.count.Inc()
}

// ServerMetrics records unary gRPC server call metrics.
type ServerMetrics struct {
	buckets   []float64
	labeler   Labeler
	retention Retention
	clock     func() time.Time

	calls   *metrics.MetricMap[*callSeries]
	handled *metrics.MetricMap[*codeSeries]
}

// Factory builds a ServerMetrics bound to a caller-supplied retention
// provider.
//
// It lets the construction site own label extraction and buckets while
// the service injects its own retention provider, breaking the otherwise
// cyclic dependency between a service and its metrics collector.
type Factory func(retention Retention) *ServerMetrics

// NewFactory returns a Factory that applies the given options plus the
// retention provider supplied when the factory is invoked.
//
// Pass construction options here (labeler, buckets) but not WithRetention —
// the provider is appended at call time.
func NewFactory(options ...Option) Factory {
	return func(retention Retention) *ServerMetrics {
		opts := make([]Option, 0, len(options)+1)
		opts = append(opts, options...)
		opts = append(opts, WithRetention(retention))
		return New(opts...)
	}
}

// New creates a new ServerMetrics with the supplied options.
func New(options ...Option) *ServerMetrics {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}
	return &ServerMetrics{
		buckets:   opts.Buckets,
		labeler:   opts.Labeler,
		retention: opts.Retention,
		clock:     opts.Clock,
		calls:     metrics.NewMetricMap[*callSeries](),
		handled:   metrics.NewMetricMap[*codeSeries](),
	}
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that records
// started, handled, and handling-latency metrics for every call.
func (m *ServerMetrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp any, err error) {
		start := m.clock()

		extra := m.labeler(info.FullMethod, req)

		service, method := splitFullMethod(info.FullMethod)
		callID := m.callID(service, method, extra)
		call := m.calls.GetOrCreate(callID, func() *callSeries {
			return newCallSeries(m.buckets)
		})
		call.RecordStart()

		defer func() {
			r := recover()
			var code codes.Code
			if r != nil {
				code = codes.Unknown
			} else {
				code = status.Code(err)
			}
			m.recordExit(call, service, method, extra, code, start)
			if r != nil {
				panic(r)
			}
		}()

		resp, err = handler(ctx, req)
		return resp, err
	}
}

func (m *ServerMetrics) recordExit(
	call *callSeries,
	service string,
	method string,
	extra metrics.Labels,
	code codes.Code,
	start time.Time,
) {
	durationSeconds := m.clock().Sub(start).Seconds()
	call.RecordFinish(durationSeconds)

	handledID := m.handledID(service, method, code.String(), extra)
	handled := m.handled.GetOrCreate(handledID, func() *codeSeries {
		return &codeSeries{}
	})
	handled.Record()
}

// Collect returns the current snapshot of all metric series as commonpb.Metric
// values, pruning stale series first.
func (m *ServerMetrics) Collect() []*commonpb.Metric {
	m.reconcile()

	var out []*commonpb.Metric
	for _, entry := range m.calls.Metrics() {
		out = entry.Value.AppendTo(out, commonpb.MetricLabelsToProto(entry.ID.Labels))
	}
	for _, entry := range m.handled.Metrics() {
		out = entry.Value.AppendTo(out, commonpb.MetricLabelsToProto(entry.ID.Labels))
	}

	return out
}

// reconcile prunes series the retention predicate no longer keeps.
func (m *ServerMetrics) reconcile() {
	keep := m.retention()
	m.calls.DeleteWhere(func(id metrics.MetricID, _ *callSeries) bool {
		return !keep(id)
	})
	m.handled.DeleteWhere(func(id metrics.MetricID, _ *codeSeries) bool {
		return !keep(id)
	})
}

func (m *ServerMetrics) callID(service, method string, extra metrics.Labels) metrics.MetricID {
	labels := metrics.Labels{
		labelType:    grpcTypeUnary,
		labelService: service,
		labelMethod:  method,
	}
	maps.Copy(labels, extra)
	return metrics.MetricID{Labels: labels}
}

func (m *ServerMetrics) handledID(service, method, code string, extra metrics.Labels) metrics.MetricID {
	labels := metrics.Labels{
		labelType:    grpcTypeUnary,
		labelService: service,
		labelMethod:  method,
		labelCode:    code,
	}
	maps.Copy(labels, extra)
	return metrics.MetricID{Labels: labels}
}

// splitFullMethod splits a gRPC full method ("/pkg.Service/Method") into its
// fully-qualified service and method names.
//
// A malformed input with no service segment yields an empty service and the
// whole remainder as the method.
func splitFullMethod(fullMethod string) (service, method string) {
	trimmed := strings.TrimPrefix(fullMethod, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[:idx], trimmed[idx+1:]
	}

	return "", trimmed
}
