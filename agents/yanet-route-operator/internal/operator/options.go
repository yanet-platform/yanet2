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

// WithReconcilerMetrics attaches the metrics observer for the reconcile
// loop.
func WithReconcilerMetrics(metrics ReconcilerMetricsObserver) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.Metrics = metrics
	}
}

type routeServiceOptions struct {
	RIBTTL    time.Duration
	OnChanged func()
	Log       *zap.Logger
}

func newRouteServiceOptions() *routeServiceOptions {
	return &routeServiceOptions{
		RIBTTL:    DefaultRIBTTL,
		OnChanged: func() {},
		Log:       zap.NewNop(),
	}
}

type RouteServiceOption func(*routeServiceOptions)

// WithRouteServiceLog sets the logger for the RouteService.
func WithRouteServiceLog(log *zap.Logger) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.Log = log
	}
}

// WithRouteServiceRIBTTL sets the TTL applied to FeedRIB cleanup tasks.
func WithRouteServiceRIBTTL(ttl time.Duration) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.RIBTTL = ttl
	}
}

// WithRouteServiceOnChanged registers a callback fired whenever the
// RIB state mutates so the reconcile loop can wake up.
func WithRouteServiceOnChanged(fn func()) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.OnChanged = fn
	}
}

type neighbourServiceOptions struct {
	OnChanged func()
}

func newNeighbourServiceOptions() *neighbourServiceOptions {
	return &neighbourServiceOptions{
		OnChanged: func() {},
	}
}

type NeighbourServiceOption func(*neighbourServiceOptions)

// WithNeighbourServiceOnChanged registers a callback fired whenever
// neighbour state mutates so the reconcile loop can wake up.
func WithNeighbourServiceOnChanged(fn func()) NeighbourServiceOption {
	return func(o *neighbourServiceOptions) {
		o.OnChanged = fn
	}
}

type metricsServiceOptions struct {
	Metrics MetricsCollector
}

func newMetricsServiceOptions() *metricsServiceOptions {
	return &metricsServiceOptions{
		Metrics: noopMetricsCollector{},
	}
}

type MetricsServiceOption func(*metricsServiceOptions)

// WithMetricsServiceCollector attaches the metrics collector that
// GetMetrics serves from. When unset, GetMetrics returns an empty
// response.
func WithMetricsServiceCollector(c MetricsCollector) MetricsServiceOption {
	return func(o *metricsServiceOptions) {
		o.Metrics = c
	}
}

type operatorServiceOptions struct{}

func newOperatorServiceOptions() *operatorServiceOptions {
	return &operatorServiceOptions{}
}

type OperatorServiceOption func(*operatorServiceOptions)

type grpcServerOptions struct {
	Log *zap.Logger
}

func newGRPCServerOptions() *grpcServerOptions {
	return &grpcServerOptions{
		Log: zap.NewNop(),
	}
}

type GRPCServerOption func(*grpcServerOptions)

// WithGRPCLog sets the logger used by the gRPC server wrapper.
func WithGRPCLog(log *zap.Logger) GRPCServerOption {
	return func(o *grpcServerOptions) {
		o.Log = log
	}
}

type gatewayActuatorOptions struct {
	Function FunctionConfig
	Log      *zap.Logger
}

func newGatewayActuatorOptions() *gatewayActuatorOptions {
	return &gatewayActuatorOptions{
		Log: zap.NewNop(),
	}
}

type GatewayActuatorOption func(*gatewayActuatorOptions)

// WithGatewayActuatorLog sets the logger for a single gateway actuator.
func WithGatewayActuatorLog(log *zap.Logger) GatewayActuatorOption {
	return func(o *gatewayActuatorOptions) {
		o.Log = log
	}
}

// WithGatewayActuatorFunction sets the network function the actuator
// publishes to its gateway on every Apply pass.
func WithGatewayActuatorFunction(fn FunctionConfig) GatewayActuatorOption {
	return func(o *gatewayActuatorOptions) {
		o.Function = fn
	}
}
