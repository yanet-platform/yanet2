package operator

import "context"

// ObservedActuator wraps an inner Actuator and invokes a callback with the
// per-gateway apply outcome after each Apply call.
//
// The inner error is returned unchanged so FanOutActuator error joining
// and reconcile backoff are preserved.
type ObservedActuator[T any] struct {
	inner   Actuator[T]
	id      string
	observe func(id string, err error)
}

// NewObservedActuator wraps inner with an observe callback keyed by id.
//
// After each Apply, observe is called with the gateway id and the error
// returned by the inner actuator (nil on success).
func NewObservedActuator[T any](
	inner Actuator[T],
	id string,
	observe func(string, error),
) *ObservedActuator[T] {
	return &ObservedActuator[T]{
		inner:   inner,
		id:      id,
		observe: observe,
	}
}

// Apply calls the inner actuator, reports the outcome via the observe
// callback, and returns the inner error unchanged.
func (m *ObservedActuator[T]) Apply(ctx context.Context, state T) error {
	err := m.inner.Apply(ctx, state)
	m.observe(m.id, err)
	return err
}

// Close delegates to the inner actuator.
func (m *ObservedActuator[T]) Close() error {
	return m.inner.Close()
}
