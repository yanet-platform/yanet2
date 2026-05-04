package operator

import (
	"time"

	"go.uber.org/zap"
)

type options struct {
	Log *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

type Option func(*options)

// WithLog sets the logger for the Operator and all sub-components.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

type reconcilerOptions struct {
	Interval       time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Metrics        ReconcilerMetricsObserver
	Log            *zap.Logger
}

func newReconcilerOptions() *reconcilerOptions {
	return &reconcilerOptions{
		Interval:       DefaultReconcileInterval,
		InitialBackoff: DefaultReconcileInitialBackoff,
		MaxBackoff:     DefaultReconcileMaxBackoff,
		Metrics:        noopReconcilerMetricsObserver{},
		Log:            zap.NewNop(),
	}
}

type ReconcilerOption func(*reconcilerOptions)

// WithReconcilerLog sets the logger used by the reconcile loop.
func WithReconcilerLog(log *zap.Logger) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Log = log
	}
}

// WithReconcileInterval sets the steady-state period between successful
// reconcile passes.
func WithReconcileInterval(d time.Duration) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Interval = d
	}
}

// WithReconcileBackoff sets the initial and maximum sleep durations for
// the exponential backoff applied after failed reconcile passes.
func WithReconcileBackoff(initial, max time.Duration) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.InitialBackoff = initial
		o.MaxBackoff = max
	}
}

func WithReconcilerMetrics(metrics ReconcilerMetricsObserver) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Metrics = metrics
	}
}

type serviceOptions struct {
	Metrics MetricsCollector
	Log     *zap.Logger
}

func newServiceOptions() *serviceOptions {
	return &serviceOptions{
		Metrics: noopMetricsCollector{},
		Log:     zap.NewNop(),
	}
}

type ServiceOption func(*serviceOptions)

func WithServiceLog(log *zap.Logger) ServiceOption {
	return func(o *serviceOptions) {
		o.Log = log
	}
}

// WithServiceMetrics attaches the metrics sink that GetMetrics serves
// from.
//
// When unset, GetMetrics returns an empty response.
func WithServiceMetrics(m MetricsCollector) ServiceOption {
	return func(o *serviceOptions) {
		o.Metrics = m
	}
}

type gatewayActuatorOptions struct {
	Metrics GatewayActuatorMetricsObserver
	Log     *zap.Logger
}

func newGatewayActuatorOptions() *gatewayActuatorOptions {
	return &gatewayActuatorOptions{
		Metrics: noopGatewayActuatorMetricsObserver{},
		Log:     zap.NewNop(),
	}
}

type GatewayActuatorOption func(*gatewayActuatorOptions)

func WithGatewayActuatorLog(log *zap.Logger) GatewayActuatorOption {
	return func(o *gatewayActuatorOptions) {
		o.Log = log
	}
}

func WithGatewayActuatorMetrics(metrics GatewayActuatorMetricsObserver) GatewayActuatorOption {
	return func(o *gatewayActuatorOptions) {
		o.Metrics = metrics
	}
}
