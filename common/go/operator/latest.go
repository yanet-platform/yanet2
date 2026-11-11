package operator

import (
	"sync"
)

// Version is one published value of the desired state.
type Version[T any] struct {
	// Value is the published state; nobody changes it afterwards.
	Value T
	// Number counts the publications from one; zero means nothing was
	// published yet.
	Number uint64
	// Replaced is closed once a newer value is published.
	Replaced <-chan struct{}
}

// Latest holds the newest published desired state and tells its readers
// when a newer one replaces it.
//
// A published value is immutable: neither its publisher nor a reader
// changes it. A reader that lags behind sees only the newest value, never
// the ones published in between.
type Latest[T any] struct {
	mu      sync.Mutex
	current Version[T]
	// replaced is the channel of the current version, closed by the next
	// publication.
	replaced chan struct{}
	// loaded is closed by the first load of the current version.
	loaded chan struct{}
	// taken tells whether the current version was loaded.
	taken bool
}

// NewLatest returns a Latest with nothing published yet.
func NewLatest[T any]() *Latest[T] {
	replaced := make(chan struct{})
	return &Latest[T]{
		current:  Version[T]{Replaced: replaced},
		replaced: replaced,
		loaded:   make(chan struct{}),
	}
}

// Publish makes the value the newest version and wakes the readers of the
// previous one.
func (m *Latest[T]) Publish(value T) {
	m.mu.Lock()
	defer m.mu.Unlock()

	replaced := make(chan struct{})
	close(m.replaced)
	m.replaced = replaced
	m.loaded = make(chan struct{})
	m.taken = false
	m.current = Version[T]{
		Value:    value,
		Number:   m.current.Number + 1,
		Replaced: replaced,
	}
}

// Load returns the newest version.
//
// The first load of a version tells the publisher that a reader took it,
// see Loaded.
func (m *Latest[T]) Load() Version[T] {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current.Number > 0 && !m.taken {
		m.taken = true
		close(m.loaded)
	}
	return m.current
}

// Loaded returns a channel closed once a reader loaded the newest version.
func (m *Latest[T]) Loaded() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.loaded
}
