package operator

import (
	"context"
	"time"
)

// RebuildWindow is the quiet time after a build before the next one may
// start.
//
// A change after a quiet spell is built at once; changes coming faster are
// folded into one build when the window ends.
const RebuildWindow = 200 * time.Millisecond

// Rebuilder publishes the desired state that a build function assembles
// from sources changing on their own: once when it starts, then after
// changes, at most once per window.
//
// A build starts only once a reader loaded the previous one, so the builds
// keep pace with the fastest reader and every reader shares them.
type Rebuilder[T any] struct {
	build   func() T
	latest  *Latest[T]
	changed chan struct{}
}

// NewRebuilder returns a Rebuilder publishing what build returns.
func NewRebuilder[T any](build func() T) *Rebuilder[T] {
	return &Rebuilder[T]{
		build:   build,
		latest:  NewLatest[T](),
		changed: make(chan struct{}, 1),
	}
}

// Latest returns the desired state the rebuilder publishes.
func (m *Rebuilder[T]) Latest() *Latest[T] {
	return m.latest
}

// Invalidate tells the rebuilder that the sources changed.
//
// It never blocks, and changes reported before the next build starts make
// one build.
func (m *Rebuilder[T]) Invalidate() {
	select {
	case m.changed <- struct{}{}:
	default:
	}
}

// Run builds and publishes the desired state until the context ends.
func (m *Rebuilder[T]) Run(ctx context.Context) error {
	for {
		m.latest.Publish(m.build())
		if err := m.awaitNextBuild(ctx); err != nil {
			return err
		}
	}
}

// awaitNextBuild waits for the window after a build to end, then for a
// change, then for a reader to load the build.
func (m *Rebuilder[T]) awaitNextBuild(ctx context.Context) error {
	window := time.NewTimer(RebuildWindow)
	defer window.Stop()
	loaded := m.latest.Loaded()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-window.C:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.changed:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-loaded:
		return nil
	}
}
