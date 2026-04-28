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
	Log            *zap.Logger
	Interval       time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func newReconcilerOptions() *reconcilerOptions {
	return &reconcilerOptions{
		Log:            zap.NewNop(),
		Interval:       DefaultReconcileInterval,
		InitialBackoff: DefaultReconcileInitialBackoff,
		MaxBackoff:     DefaultReconcileMaxBackoff,
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

type serviceOptions struct {
	Log *zap.Logger
}

func newServiceOptions() *serviceOptions {
	return &serviceOptions{
		Log: zap.NewNop(),
	}
}

type ServiceOption func(*serviceOptions)

func WithServiceLog(log *zap.Logger) ServiceOption {
	return func(o *serviceOptions) {
		o.Log = log
	}
}

type gatewayActuatorOptions struct {
	Log *zap.Logger
}

func newGatewayActuatorOptions() *gatewayActuatorOptions {
	return &gatewayActuatorOptions{
		Log: zap.NewNop(),
	}
}

type GatewayActuatorOption func(*gatewayActuatorOptions)

func WithGatewayActuatorLog(log *zap.Logger) GatewayActuatorOption {
	return func(o *gatewayActuatorOptions) {
		o.Log = log
	}
}

type fanOutActuatorOptions struct {
	Log *zap.Logger
}

func newFanOutActuatorOptions() *fanOutActuatorOptions {
	return &fanOutActuatorOptions{
		Log: zap.NewNop(),
	}
}

type FanOutActuatorOption func(*fanOutActuatorOptions)

func WithFanOutActuatorLog(log *zap.Logger) FanOutActuatorOption {
	return func(o *fanOutActuatorOptions) {
		o.Log = log
	}
}
