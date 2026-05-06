package operator

import (
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/commonpb"
)

type options struct {
	Log *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// Option configures NewOperator.
type Option func(*options)

// WithLog sets the logger for the Operator and all sub-components.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

type routeServiceOptions struct {
	RIBs      *RIBStore
	RIBTTL    time.Duration
	OnChanged func()
	Log       *zap.Logger
}

func newRouteServiceOptions() *routeServiceOptions {
	return &routeServiceOptions{
		RIBs:      newRIBStore(zap.NewNop()),
		RIBTTL:    DefaultRIBTTL,
		OnChanged: func() {},
		Log:       zap.NewNop(),
	}
}

// RouteServiceOption configures NewRouteService.
type RouteServiceOption func(*routeServiceOptions)

// WithRouteServiceRIBStore injects an explicit shared RIB storage instance.
func WithRouteServiceRIBStore(store *RIBStore) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.RIBs = store
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

// WithRouteServiceLog sets the logger for the RouteService.
func WithRouteServiceLog(log *zap.Logger) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.Log = log
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

// NeighbourServiceOption configures NewNeighbourService.
type NeighbourServiceOption func(*neighbourServiceOptions)

// WithNeighbourServiceOnChanged registers a callback fired whenever
// neighbour state mutates so the reconcile loop can wake up.
func WithNeighbourServiceOnChanged(fn func()) NeighbourServiceOption {
	return func(o *neighbourServiceOptions) {
		o.OnChanged = fn
	}
}

// noopMetricsCollector is the default MetricsCollector wired into
// metrics-service options when the caller does not pass a real one.
type noopMetricsCollector struct{}

func (noopMetricsCollector) Collect() []*commonpb.Metric {
	return nil
}

type metricsServiceOptions struct {
	Metrics MetricsCollector
}

func newMetricsServiceOptions() *metricsServiceOptions {
	return &metricsServiceOptions{
		Metrics: noopMetricsCollector{},
	}
}

// MetricsServiceOption configures NewMetricsService.
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

// OperatorServiceOption configures NewRouteOperatorService.
type OperatorServiceOption func(*operatorServiceOptions)

type gatewayActuatorOptions struct {
	Function FunctionConfig
	Log      *zap.Logger
}

func newGatewayActuatorOptions() *gatewayActuatorOptions {
	return &gatewayActuatorOptions{
		Log: zap.NewNop(),
	}
}

// GatewayActuatorOption configures NewGatewayActuator.
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
