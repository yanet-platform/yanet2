package operator

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/readiness"
)

// Target is one gateway the operator reconciles.
type Target[T any] struct {
	// Name labels the gateway in metrics.
	Name string
	// Actuator applies the desired state to the gateway.
	Actuator Actuator[T]
	// Scope is the readiness scope every apply outcome is recorded in, empty
	// for a target that reports its readiness itself.
	Scope string
}

// targetApplier applies the desired state to one target and records how
// every apply went in the target's metrics and readiness scope.
type targetApplier[T any] struct {
	target  Target[T]
	metrics *applyMetrics
	// tracker is nil when the operator reports no readiness.
	tracker *readiness.Tracker
}

// Apply applies the state to the target and records the outcome.
func (m *targetApplier[T]) Apply(ctx context.Context, state T) error {
	start := time.Now()
	err := m.target.Actuator.Apply(ctx, state)
	m.metrics.Observe(time.Since(start), err)
	if m.tracker != nil && m.target.Scope != "" {
		m.tracker.Observe(m.target.Scope, err)
	}

	return err
}

// Close closes the target's actuator.
func (m *targetApplier[T]) Close() error {
	return m.target.Actuator.Close()
}

// fanOut applies the desired state to every target at once and joins the
// failures.
type fanOut[T any] struct {
	appliers []*targetApplier[T]
}

// Apply applies the state to every target concurrently.
func (m *fanOut[T]) Apply(ctx context.Context, state T) error {
	errs := make([]error, len(m.appliers))

	var wg errgroup.Group
	for idx, applier := range m.appliers {
		wg.Go(func() error {
			errs[idx] = applier.Apply(ctx, state)
			return nil
		})
	}
	_ = wg.Wait()

	return errors.Join(errs...)
}

// Close closes every target's actuator and joins the failures.
func (m *fanOut[T]) Close() error {
	var err error
	for _, applier := range m.appliers {
		err = errors.Join(err, applier.Close())
	}

	return err
}
