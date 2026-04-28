package operator

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// FanOutActuator applies a StageConfig to several Actuators concurrently.
//
// Apply waits for every underlying Actuator to complete and returns the
// first non-nil error observed. If any underlying Actuator fails the stage
// is considered "not applied" by the reconciler.
type FanOutActuator struct {
	actuators []Actuator

	log *zap.Logger
}

func NewFanOutActuator(
	actuators []Actuator,
	options ...FanOutActuatorOption,
) *FanOutActuator {
	opts := newFanOutActuatorOptions()
	for _, o := range options {
		o(opts)
	}

	return &FanOutActuator{
		actuators: actuators,
		log:       opts.Log,
	}
}

// Apply runs Apply on every underlying Actuator concurrently.
//
// We deliberately use errgroup.Group without a derived context: every
// underlying Apply runs to completion regardless of whether a sibling
// failed, so each gateway lands in a deterministic state instead of a
// half-applied one.
func (m *FanOutActuator) Apply(ctx context.Context, stage *StageConfig) error {
	wg := errgroup.Group{}
	for _, a := range m.actuators {
		wg.Go(func() error {
			return a.Apply(ctx, stage)
		})
	}

	if err := wg.Wait(); err != nil {
		return err
	}

	m.log.Info("applied stage to gateways",
		zap.String("stage", stage.Name),
	)

	return nil
}

// Close closes every underlying Actuator serially and joins their errors.
//
// Each Actuator is closed regardless of whether a sibling failed, so one
// bad close does not mask others.
func (m *FanOutActuator) Close() error {
	var errs []error
	for _, a := range m.actuators {
		if err := a.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
