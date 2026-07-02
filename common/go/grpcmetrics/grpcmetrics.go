// Package grpcmetrics provides opt-in gRPC unary and stream server
// interceptors that record per-call metrics and render them to
// commonpb.Metric.
//
// Each ServerMetrics instance tracks three metric families:
//   - grpc_server_started_total    — counter per {grpc_type, grpc_service, grpc_method[, config]}
//   - grpc_server_handled_total    — counter per {grpc_type, grpc_service, grpc_method, grpc_code[, config]}
//   - grpc_server_handling_seconds — histogram per {grpc_type, grpc_service, grpc_method[, config]}
//
// Label names follow the go-grpc-prometheus (go-grpc-middleware/providers/prometheus)
// server convention. grpc_type is "unary" for UnaryServerInterceptor calls and
// one of "client_stream", "server_stream", or "bidi_stream" for
// StreamServerInterceptor calls, depending on the direction flags on
// grpc.StreamServerInfo. The optional "config" label is emitted when the
// Labeler provides it.
//
// A transparent gRPC proxy (such as the gateway's unknown-service handler)
// registers every proxied method, including originally unary ones, as a
// bidirectional stream at the transport level. Calls recorded through such a
// proxy therefore carry grpc_type="bidi_stream" even when the underlying RPC
// is unary — this reflects transport-level truth, not a labeling bug.
//
// A service filter rejects calls for unknown services before any series or
// per-service method bookkeeping is allocated, and stale series are pruned
// via a retention predicate.
package grpcmetrics

import (
	"context"
	"maps"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

const (
	labelType   = "grpc_type"
	labelMethod = "grpc_method"
	labelCode   = "grpc_code"
)

// methodOverflow is the grpc_method label value recorded once a service's
// distinct method count has reached the WithPerServiceMethodLimit cap.
const methodOverflow = "other"

// ServiceLabel is the label key holding the fully-qualified gRPC service name
// on every recorded series.
//
// Retention predicates that key off the proxied service name, rather than a
// Labeler-supplied label, read id.Labels[ServiceLabel].
const ServiceLabel = "grpc_service"

const (
	grpcTypeUnary        = "unary"
	grpcTypeClientStream = "client_stream"
	grpcTypeServerStream = "server_stream"
	grpcTypeBidiStream   = "bidi_stream"
)

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
// extra labels are attached. Stream calls have no single request message, so
// StreamServerInterceptor invokes the Labeler with a nil req; a Labeler that
// only type-switches on req (the common case) naturally yields no extra
// labels for streams.
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
	buckets               []float64
	labeler               Labeler
	retention             Retention
	clock                 func() time.Time
	perServiceMethodLimit int
	serviceFilter         func(service string) bool

	calls   *metrics.MetricMap[*callSeries]
	handled *metrics.MetricMap[*codeSeries]

	methodsMu sync.Mutex
	methods   map[string]map[string]struct{}
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
		buckets:               opts.Buckets,
		labeler:               opts.Labeler,
		retention:             opts.Retention,
		clock:                 opts.Clock,
		perServiceMethodLimit: opts.PerServiceMethodLimit,
		serviceFilter:         opts.ServiceFilter,
		calls:                 metrics.NewMetricMap[*callSeries](),
		handled:               metrics.NewMetricMap[*codeSeries](),
		methods:               map[string]map[string]struct{}{},
	}
}

// resolveMethod applies the per-service method cardinality cap and returns
// the label value to record under grpc_method.
//
// The decision is made once per call and reused for the started, handled,
// and handling series alike. A method already tracked for the service keeps
// its own name. A new method is admitted while the service is under the
// limit; once the limit is reached, further new methods are folded into the
// methodOverflow label instead. A limit of 0 disables the cap.
func (m *ServerMetrics) resolveMethod(service, method string) string {
	if m.perServiceMethodLimit <= 0 {
		return method
	}

	m.methodsMu.Lock()
	defer m.methodsMu.Unlock()

	seen := m.methods[service]
	if _, ok := seen[method]; ok {
		return method
	}
	if len(seen) >= m.perServiceMethodLimit {
		return methodOverflow
	}

	if seen == nil {
		seen = map[string]struct{}{}
		m.methods[service] = seen
	}
	seen[method] = struct{}{}

	return method
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
		service, method := splitFullMethod(info.FullMethod)
		if !m.serviceFilter(service) {
			return handler(ctx, req)
		}

		start := m.clock()

		extra := m.labeler(info.FullMethod, req)

		method = m.resolveMethod(service, method)
		callID := m.callID(grpcTypeUnary, service, method, extra)
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
			m.recordExit(grpcTypeUnary, call, service, method, extra, code, start)
			if r != nil {
				panic(r)
			}
		}()

		resp, err = handler(ctx, req)
		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor that
// records started, handled, and handling-latency metrics for every call.
//
// The grpc_type label is derived from the direction flags on
// grpc.StreamServerInfo; see the package doc for the transparent-proxy
// caveat.
func (m *ServerMetrics) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		service, method := splitFullMethod(info.FullMethod)
		if !m.serviceFilter(service) {
			return handler(srv, stream)
		}

		start := m.clock()

		grpcType := streamGRPCType(info)
		extra := m.labeler(info.FullMethod, nil)

		method = m.resolveMethod(service, method)
		callID := m.callID(grpcType, service, method, extra)
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
			m.recordExit(grpcType, call, service, method, extra, code, start)
			if r != nil {
				panic(r)
			}
		}()

		err = handler(srv, stream)
		return err
	}
}

// streamGRPCType classifies a stream call per the go-grpc-prometheus
// convention: bidirectional when both direction flags are set, otherwise the
// single direction that is set.
func streamGRPCType(info *grpc.StreamServerInfo) string {
	switch {
	case info.IsClientStream && info.IsServerStream:
		return grpcTypeBidiStream
	case info.IsClientStream:
		return grpcTypeClientStream
	case info.IsServerStream:
		return grpcTypeServerStream
	default:
		return grpcTypeBidiStream
	}
}

func (m *ServerMetrics) recordExit(
	grpcType string,
	call *callSeries,
	service string,
	method string,
	extra metrics.Labels,
	code codes.Code,
	start time.Time,
) {
	durationSeconds := m.clock().Sub(start).Seconds()
	call.RecordFinish(durationSeconds)

	handledID := m.handledID(grpcType, service, method, code.String(), extra)
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

func (m *ServerMetrics) callID(grpcType, service, method string, extra metrics.Labels) metrics.MetricID {
	labels := metrics.Labels{
		labelType:    grpcType,
		ServiceLabel: service,
		labelMethod:  method,
	}
	maps.Copy(labels, extra)
	return metrics.MetricID{Labels: labels}
}

func (m *ServerMetrics) handledID(grpcType, service, method, code string, extra metrics.Labels) metrics.MetricID {
	labels := metrics.Labels{
		labelType:    grpcType,
		ServiceLabel: service,
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
