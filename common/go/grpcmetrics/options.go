package grpcmetrics

import (
	"time"

	"github.com/yanet-platform/yanet2/common/go/metrics"
)

// Option configures a ServerMetrics instance.
type Option func(*options)

type options struct {
	Buckets   []float64
	Labeler   Labeler
	Retention Retention
	Clock     func() time.Time
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
		Clock: time.Now,
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
