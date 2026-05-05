package operator

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type fanOutOptions struct {
	Log *zap.Logger
}

func newFanOutOptions() *fanOutOptions {
	return &fanOutOptions{
		Log: zap.NewNop(),
	}
}

// FanOutOption configures NewFanOutActuator.
type FanOutOption func(*fanOutOptions)

// WithFanOutLog sets the logger for the fan-out actuator.
func WithFanOutLog(log *zap.Logger) FanOutOption {
	return func(o *fanOutOptions) {
		o.Log = log
	}
}

// Actuator applies a desired state of type T to a single downstream
// target, typically a single gateway.
type Actuator[T any] interface {
	Apply(ctx context.Context, state T) error
	Close() error
}

// FanOutActuator applies state to several Actuators concurrently.
type FanOutActuator[T any] struct {
	actuators []Actuator[T]
	log       *zap.Logger
}

// NewFanOutActuator constructs a fan-out actuator from a slice of
// underlying actuators.
func NewFanOutActuator[T any](
	actuators []Actuator[T],
	options ...FanOutOption,
) *FanOutActuator[T] {
	opts := newFanOutOptions()
	for _, o := range options {
		o(opts)
	}

	return &FanOutActuator[T]{
		actuators: actuators,
		log:       opts.Log,
	}
}

// Apply runs Apply on every underlying Actuator concurrently and joins
// all observed errors.
func (m *FanOutActuator[T]) Apply(ctx context.Context, state T) error {
	errs := make([]error, len(m.actuators))

	var wg errgroup.Group
	for idx, a := range m.actuators {
		wg.Go(func() error {
			errs[idx] = a.Apply(ctx, state)
			return nil
		})
	}
	_ = wg.Wait()

	if err := errors.Join(errs...); err != nil {
		return err
	}

	m.log.Debug("fan-out actuator apply complete",
		zap.Int("actuator_count", len(m.actuators)),
	)
	return nil
}

// Close closes every underlying Actuator serially and joins their
// errors.
func (m *FanOutActuator[T]) Close() error {
	var out error
	for _, a := range m.actuators {
		if err := a.Close(); err != nil {
			out = errors.Join(out, err)
		}
	}

	return out
}
