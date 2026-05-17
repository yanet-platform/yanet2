package operator

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

type grpcServerOption struct {
	cfg      *GRPCServerConfig
	services []ServiceRegistrar
}

type options struct {
	Reconcile  ReconcileConfig
	Register   RegisterConfig
	Gateways   []GatewayConfig
	Workers    []Runner
	PreRun     PreRun
	Metrics    ReconcilerMetricsObserver
	GRPCServer *grpcServerOption
	Log        *zap.Logger
}

func newOptions() *options {
	return &options{
		Reconcile: ReconcileConfig{
			Interval:       xcfg.MustNonZero(DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(DefaultReconcileMaxBackoff),
		},
		PreRun:  func(context.Context) error { return nil },
		Metrics: NoopReconcilerMetricsObserver{},
		Log:     zap.NewNop(),
	}
}

// Option configures the Operator.
type Option func(*options)

// WithLog sets the logger used by the Operator and its sub-components
// when no more specific logger is supplied.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// WithReconcile sets the reconcile-loop timing parameters.
//
// Zero-valued fields set the corresponding default value.
func WithReconcile(cfg ReconcileConfig) Option {
	return func(o *options) {
		if cfg.Interval.Unwrap() > 0 {
			o.Reconcile.Interval = cfg.Interval
		}
		if cfg.InitialBackoff.Unwrap() > 0 {
			o.Reconcile.InitialBackoff = cfg.InitialBackoff
		}
		if cfg.MaxBackoff.Unwrap() > 0 {
			o.Reconcile.MaxBackoff = cfg.MaxBackoff
		}
	}
}

// WithGateways enables the gateway-registration runner against the
// supplied gateways using the supplied registration heartbeat config.
//
// When no gateways are provided the registration runner is not started.
//
// Registration requires WithGRPCServer.
func WithGateways(register RegisterConfig, gateways ...GatewayConfig) Option {
	return func(o *options) {
		o.Register = register
		o.Gateways = gateways
	}
}

// WithGRPCServer enables an embedded gRPC server bound to cfg.Endpoint
// that exposes the supplied service set.
//
// Gateway registration via WithGateways is optional; use gRPC alone for
// metrics or operator APIs without director registration.
//
// When this option is not supplied, the operator does not bind a listener.
func WithGRPCServer(cfg *GRPCServerConfig, services ...ServiceRegistrar) Option {
	return func(o *options) {
		o.GRPCServer = &grpcServerOption{
			cfg:      cfg,
			services: services,
		}
	}
}

// WithWorkers attaches additional long-running goroutines to the
// operator's errgroup.
//
// Returning a non-nil error from any worker tears down the operator.
func WithWorkers(workers ...Runner) Option {
	return func(o *options) {
		o.Workers = workers
	}
}

// WithPreRun registers a hook executed once before any goroutine starts.
//
// Useful for seeding the source from static configuration.
func WithPreRun(fn PreRun) Option {
	return func(o *options) {
		o.PreRun = fn
	}
}

// WithMetrics attaches the metrics observer for the reconcile loop.
func WithMetrics(observer ReconcilerMetricsObserver) Option {
	return func(o *options) {
		o.Metrics = observer
	}
}

// NoopReconcilerMetricsObserver is the default observer wired into
// reconciler options when no metrics sink is attached.
type NoopReconcilerMetricsObserver struct{}

func (NoopReconcilerMetricsObserver) OnReconcileCompleted(error)       {}
func (NoopReconcilerMetricsObserver) OnBackoffScheduled(time.Duration) {}
func (NoopReconcilerMetricsObserver) OnBackoffReset()                  {}
func (NoopReconcilerMetricsObserver) OnStateChanged(ReconcilerState)   {}

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
		Metrics:        NoopReconcilerMetricsObserver{},
		Log:            zap.NewNop(),
	}
}

// ReconcilerOption configures NewReconciler.
type ReconcilerOption func(*reconcilerOptions)

// WithReconcilerLog sets the logger used by the reconcile loop.
func WithReconcilerLog(log *zap.Logger) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Log = log
	}
}

// WithReconcileInterval sets the steady-state period between
// successful reconcile passes.
func WithReconcileInterval(d time.Duration) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Interval = d
	}
}

// WithReconcileBackoff sets the initial and maximum sleep durations
// for the exponential backoff applied after failed reconcile passes.
func WithReconcileBackoff(initial, max time.Duration) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.InitialBackoff = initial
		o.MaxBackoff = max
	}
}

// WithReconcilerMetrics attaches the metrics observer for the
// reconcile loop.
func WithReconcilerMetrics(metrics ReconcilerMetricsObserver) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Metrics = metrics
	}
}

type functionApplierOptions struct {
	IgnorePdump bool
}

func newFunctionApplierOptions() *functionApplierOptions {
	return &functionApplierOptions{}
}

// FunctionApplierOption configures NewFunctionApplier.
type FunctionApplierOption func(*functionApplierOptions)

// WithIgnorePdump sets the chain-modules comparison strategy: when enabled,
// pdump modules on the gateway are ignored when checking whether the chain
// already matches spec.Modules.
func WithIgnorePdump(enabled bool) FunctionApplierOption {
	return func(o *functionApplierOptions) {
		o.IgnorePdump = enabled
	}
}
