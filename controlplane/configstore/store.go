package configstore

import (
	"errors"
	"maps"
	"slices"
	"sync"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ErrNotFound is reported by Delete when nothing is published under the
// name.
var ErrNotFound = errors.New("config not found")

// Entry is a published configuration owned by a Store.
type Entry interface {
	// Free releases the entry, reporting ffi.ErrStillReferenced when a
	// live configuration generation still references it.
	Free() error
}

// Store owns the published configurations of one control-plane service,
// keyed by name, together with the entries whose free was refused.
//
// Readers never wait for shared memory: a lookup takes a read lock that
// no writer holds across a publish, so a slow update or delete stalls
// only other writers. Writers run one at a time across every name, and
// the entry a writer is handed stays the published one until its own
// publish replaces it. A superseded or deleted entry whose free is
// refused, because a live generation still references it, is parked and
// retried on every later successful mutation, and the store is the only
// place that remembers it.
//
// A published entry is handed to readers without any lock, so it must
// not change after it is stored. Mutation callbacks must not call back
// into a mutation of the same store, which would deadlock, while
// lookups from a callback are fine.
type Store[E Entry] struct {
	// writeMu serializes mutations for their whole duration, publish
	// included, and guards the parked entries.
	writeMu sync.Mutex
	// mu guards the entries map alone and is never held across a
	// publish or a free.
	mu      sync.RWMutex
	entries map[string]E
	// deferred holds retired entries whose free was refused.
	deferred []E
}

// NewStore returns an empty store.
func NewStore[E Entry]() *Store[E] {
	return &Store[E]{entries: map[string]E{}}
}

// Names returns the published names in sorted order.
func (m *Store[E]) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return slices.Sorted(maps.Keys(m.entries))
}

// Get returns the entry published under the name.
func (m *Store[E]) Get(name string) (E, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.entries[name]
	return entry, ok
}

// Update publishes a new entry under the name and retires the previous
// one.
//
// The callback is handed the current entry, when one exists, and returns
// the entry to store. Its error is returned unchanged and leaves the
// store as it was. On success the new entry replaces the old one under a
// brief write lock, then the parked entries are retried and the old one
// is freed or parked, so a callback must build a fresh entry rather than
// hand back the one it was given. Callbacks run one at a time across
// every name.
func (m *Store[E]) Update(
	name string,
	publish func(current E, ok bool) (E, error),
) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	current, ok := m.Get(name)
	entry, err := publish(current, ok)
	if err != nil {
		return err
	}

	m.set(name, entry)

	// The publish retired the generation holding the previous entry.
	m.reclaimDeferred()
	if ok {
		m.parkOrFree(current)
	}

	return nil
}

// Delete unpublishes the entry under the name and forgets it.
//
// ErrNotFound is returned when the name is absent, before the callback
// runs. The callback's error is returned unchanged and keeps the entry
// published. On success the entry is removed under a brief write lock,
// then the parked entries are retried and the entry is freed or parked.
func (m *Store[E]) Delete(name string, unpublish func(current E) error) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	current, ok := m.Get(name)
	if !ok {
		return ErrNotFound
	}
	if err := unpublish(current); err != nil {
		return err
	}

	m.remove(name)

	// The delete retired the generation holding the entry.
	m.reclaimDeferred()
	m.parkOrFree(current)

	return nil
}

// ReclaimDeferred retries every parked entry, dropping the ones whose
// generations have drained and keeping the rest parked.
//
// Every successful mutation runs it, and anything else may call it at
// any time.
func (m *Store[E]) ReclaimDeferred() {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	m.reclaimDeferred()
}

func (m *Store[E]) set(name string, entry E) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[name] = entry
}

func (m *Store[E]) remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.entries, name)
}

// parkOrFree frees a retired entry and parks it when the free is
// refused. The caller must hold the write lock.
func (m *Store[E]) parkOrFree(entry E) {
	if err := entry.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, entry)
	}
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold the write lock.
func (m *Store[E]) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, entry := range m.deferred {
		if err := entry.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, entry)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
