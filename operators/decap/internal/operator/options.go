package operator

import "go.uber.org/zap"

// options holds operator-level configuration.
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

// gatewayActuatorOptions holds configuration for a single GatewayActuator.
type gatewayActuatorOptions struct {
	Log *zap.Logger
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

// staticSourceOptions holds configuration for the staticSource.
type staticSourceOptions struct {
	Log *zap.Logger
}

func newStaticSourceOptions() *staticSourceOptions {
	return &staticSourceOptions{
		Log: zap.NewNop(),
	}
}

// StaticSourceOption configures NewStaticSource.
type StaticSourceOption func(*staticSourceOptions)

// WithSourceLog sets the logger for the staticSource.
func WithSourceLog(log *zap.Logger) StaticSourceOption {
	return func(o *staticSourceOptions) {
		o.Log = log
	}
}
