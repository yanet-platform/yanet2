package operator

import (
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

// noopMetricsCollector is the default MetricsCollector wired into
// service options when the caller does not pass a real one.
type noopMetricsCollector struct{}

func (noopMetricsCollector) Collect() []*commonpb.Metric {
	return nil
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

// ServiceOption configures NewService.
type ServiceOption func(*serviceOptions)

// WithServiceLog sets the logger for the Service.
func WithServiceLog(log *zap.Logger) ServiceOption {
	return func(o *serviceOptions) {
		o.Log = log
	}
}

// WithServiceMetrics attaches the metrics sink that GetMetrics serves
// from. When unset, GetMetrics returns an empty response.
func WithServiceMetrics(m MetricsCollector) ServiceOption {
	return func(o *serviceOptions) {
		o.Metrics = m
	}
}

// noopGatewayActuatorMetricsObserver is the default
// GatewayActuatorMetricsObserver wired into gateway-actuator options
// when no metrics sink is attached.
type noopGatewayActuatorMetricsObserver struct{}

func (noopGatewayActuatorMetricsObserver) OnApplyCompleted(error)          {}
func (noopGatewayActuatorMetricsObserver) OnResourceUpdated(string, error) {}
func (noopGatewayActuatorMetricsObserver) OnGC(int, int, error)            {}

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

// GatewayActuatorOption configures NewGatewayActuator.
type GatewayActuatorOption func(*gatewayActuatorOptions)

// WithGatewayActuatorLog sets the logger for the gateway actuator.
func WithGatewayActuatorLog(log *zap.Logger) GatewayActuatorOption {
	return func(o *gatewayActuatorOptions) {
		o.Log = log
	}
}

// WithGatewayActuatorMetrics attaches the per-gateway metrics observer.
func WithGatewayActuatorMetrics(metrics GatewayActuatorMetricsObserver) GatewayActuatorOption {
	return func(o *gatewayActuatorOptions) {
		o.Metrics = metrics
	}
}
