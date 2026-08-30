package acl

import (
	"context"
	"sync"

	"golang.org/x/sync/singleflight"
)

// metricsFlight coalesces concurrent collections of one metric family
// into a single in-flight collection.
//
// A request arriving with nothing in progress starts the collection; a
// request arriving while one is in progress waits for it instead of
// starting its own, and every waiter of one collection receives the
// identical values or the identical error. No stale snapshot is ever
// served: a request arriving after a completed collection starts a new
// one. A waiter whose context expires during the wait returns the
// context error while the collection runs to completion for the other
// waiters. A delivered result is shared by all waiters of its collection
// and must be treated as read-only by every one of them. Releasing the
// memory a collection still reads is only safe after a drain.
type metricsFlight[T any] struct {
	key string

	group singleflight.Group

	// mu guards current, the completion signal of the registered
	// collection: nil while no collection is registered, a channel
	// closed once the registered collection's read has finished. A
	// request finding nil registers itself and forgets the group's key,
	// so the delivery tail of a finished collection cannot absorb it as
	// a waiter of values it did not collect.
	mu      sync.Mutex
	current chan struct{}
}

func newMetricsFlight[T any](key string) metricsFlight[T] {
	return metricsFlight[T]{key: key}
}

// Do runs the collection at most once per in-flight request and hands
// its result to every concurrent caller.
//
// The context bounds only the wait: a caller whose context expires
// before the collection finishes is released with the context error,
// and the collection itself runs to completion for its other waiters.
func (m *metricsFlight[T]) Do(ctx context.Context, collect func() (T, error)) (T, error) {
	m.mu.Lock()
	if m.current == nil {
		m.group.Forget(m.key)
		m.current = make(chan struct{})
	}
	m.mu.Unlock()

	channel := m.group.DoChan(m.key, func() (any, error) {
		defer m.finishFlight()
		return collect()
	})

	select {
	case result := <-channel:
		if result.Err != nil {
			var zero T
			return zero, result.Err
		}
		return result.Val.(T), nil
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Drain blocks until the in-flight collection has finished.
//
// A collection outlives the request handlers that joined it, so
// releasing the memory its read still touches must wait for it.
func (m *metricsFlight[T]) Drain() {
	m.mu.Lock()
	current := m.current
	m.mu.Unlock()

	if current != nil {
		<-current
	}
}

func (m *metricsFlight[T]) finishFlight() {
	m.mu.Lock()
	current := m.current
	m.current = nil
	m.mu.Unlock()

	if current != nil {
		close(current)
	}
}
