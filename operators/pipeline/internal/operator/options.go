package operator

import (
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

// Option configures NewOperator.
type Option func(*options)

// WithLog sets the logger for the Operator and all sub-components.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
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

// ServiceOption configures NewService.
type ServiceOption func(*serviceOptions)

// WithServiceLog sets the logger for the Service.
func WithServiceLog(log *zap.Logger) ServiceOption {
	return func(o *serviceOptions) {
		o.Log = log
	}
}

// noopGatewayActuatorMetricsObserver is the default
// GatewayActuatorMetricsObserver wired into gateway-actuator options
// when no metrics sink is attached.
type noopGatewayActuatorMetricsObserver struct{}

func (noopGatewayActuatorMetricsObserver) OnResourceUpdated(string, error) {}
func (noopGatewayActuatorMetricsObserver) OnGC(int, int, error)            {}

// noopGatewayActuatorStageObserver is the default
// GatewayActuatorStageObserver wired into gateway-actuator options when no
// stage queue listens.
type noopGatewayActuatorStageObserver struct{}

func (noopGatewayActuatorStageObserver) OnStageApplying(string, *StageConfig)       {}
func (noopGatewayActuatorStageObserver) OnStageApplied(string, *StageConfig, error) {}

type gatewayActuatorOptions struct {
	Metrics GatewayActuatorMetricsObserver
	Stages  GatewayActuatorStageObserver
	Log     *zap.Logger
}

func newGatewayActuatorOptions() *gatewayActuatorOptions {
	return &gatewayActuatorOptions{
		Metrics: noopGatewayActuatorMetricsObserver{},
		Stages:  noopGatewayActuatorStageObserver{},
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

// WithGatewayActuatorStageObserver attaches the observer of the stages the
// gateway applies.
func WithGatewayActuatorStageObserver(stages GatewayActuatorStageObserver) GatewayActuatorOption {
	return func(o *gatewayActuatorOptions) {
		o.Stages = stages
	}
}
