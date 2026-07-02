package grpcmetrics

import (
	"time"

	"github.com/yanet-platform/yanet2/common/go/metrics"
)

// Option configures a ServerMetrics instance.
type Option func(*options)

type options struct {
	Buckets               []float64
	Labeler               Labeler
	Retention             Retention
	Clock                 func() time.Time
	PerServiceMethodLimit int
	ServiceFilter         func(service string) bool
}

func newOptions() *options {
	return &options{
		Buckets: DefaultBuckets,
		Labeler: func(string, any) metrics.Labels {
			return nil
		},
		Retention: func() func(metrics.MetricID) bool {
			return func(metrics.MetricID) bool {
				return true
			}
		},
		Clock:                 time.Now,
		PerServiceMethodLimit: 0,
		ServiceFilter: func(string) bool {
			return true
		},
	}
}

// WithBuckets sets the histogram bucket boundaries in seconds.
func WithBuckets(buckets []float64) Option {
	return func(o *options) {
		o.Buckets = buckets
	}
}

// WithLabeler sets a function that derives extra labels from each request.
func WithLabeler(labeler Labeler) Option {
	return func(o *options) {
		o.Labeler = labeler
	}
}

// WithRetention sets the retention provider used to prune stale series.
//
// The provider is called once per Collect, outside the metric maps' lock.
func WithRetention(retention Retention) Option {
	return func(o *options) {
		o.Retention = retention
	}
}

// WithClock overrides the time source used for latency measurement.
//
// Primarily useful in tests for deterministic time control.
func WithClock(fn func() time.Time) Option {
	return func(o *options) {
		if fn != nil {
			o.Clock = fn
		}
	}
}

// WithPerServiceMethodLimit caps the number of distinct grpc_method label
// values tracked per grpc_service.
//
// Once a service reaches the limit, calls for a method not already seen are
// recorded under the overflow label grpc_method="other" instead of the
// client-supplied name; methods already tracked keep recording under their
// own name. A server that only exposes registered services rejects unknown
// methods before any interceptor runs, so the cap is chiefly useful for
// transparent proxies, which forward any client-supplied method name under a
// registered service straight to the backend. The default, 0, is unlimited.
func WithPerServiceMethodLimit(limit int) Option {
	return func(o *options) {
		o.PerServiceMethodLimit = limit
	}
}

// WithServiceFilter sets a predicate that gates which fully-qualified gRPC
// services get recorded at all.
//
// It runs first, before any series or per-service method bookkeeping is
// allocated for a call. This is essential for transparent proxies whose
// interceptors run ahead of routing and auth: an unauthenticated caller
// probing random service names would otherwise create a permanent metric
// series and a permanent methods-map entry for each one, since Retention
// only prunes series for services the server once knew but later removed.
// The default accepts every service.
func WithServiceFilter(filter func(service string) bool) Option {
	return func(o *options) {
		o.ServiceFilter = filter
	}
}
