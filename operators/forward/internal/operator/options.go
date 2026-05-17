package operator

import "go.uber.org/zap"

type options struct {
	Log *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// Option configures NewGatewayActuator.
type Option func(*options)

// WithLog sets the logger for the GatewayActuator.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

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
