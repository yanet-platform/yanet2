package configstore

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"

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

// entry is the writer lock of one name together with the configuration
// published under it.
//
// A record lives while a writer holds it or something is published under
// the name, so a writer of a new name has a lock to wait on.
type entry[E Entry] struct {
	// mu serializes writers of the name for their whole mutation.
	mu sync.Mutex
	// published holds the published configuration, nil when there is
	// none.
	published atomic.Pointer[E]
}

// Lock takes the writer lock of the name.
func (m *entry[E]) Lock() {
	m.mu.Lock()
}

// Unlock releases the writer lock of the name.
func (m *entry[E]) Unlock() {
	m.mu.Unlock()
}

// Published returns the configuration published under the name.
func (m *entry[E]) Published() (E, bool) {
	published := m.published.Load()
	if published == nil {
		var zero E
		return zero, false
	}

	return *published, true
}

// Publish makes the configuration the published one.
func (m *entry[E]) Publish(value E) {
	m.published.Store(&value)
}

// Unpublish forgets the published configuration.
func (m *entry[E]) Unpublish() {
	m.published.Store(nil)
}

// Store owns the published configurations of one control-plane service,
// keyed by name, together with the entries whose free was refused.
//
// Readers never wait for shared memory: a lookup takes a read lock that
// no writer holds across a publish, so a slow update or delete stalls
// only a writer of the same name. Writers of different names run their
// callbacks concurrently, and the entry a callback is handed stays the
// published one for its name until its own publish replaces it. A
// superseded or deleted entry whose free is refused, because a live
// generation still references it, is parked and retried on every later
// successful mutation of any name, and the store is the only place that
// remembers it.
//
// A published entry is handed to readers without any lock, so it must
// not change after it is stored. Mutation callbacks must not call back
// into a mutation of the same store, which would deadlock, while
// lookups from a callback are fine.
type Store[E Entry] struct {
	// mu guards the entries map alone and is never held across a
	// publish or a free.
	mu      sync.RWMutex
	entries map[string]*entry[E]

	// deferMu guards deferred. Readers never take it.
	deferMu sync.Mutex
	// deferred holds retired entries whose free was refused.
	deferred []E
}

// NewStore returns an empty store.
func NewStore[E Entry]() *Store[E] {
	return &Store[E]{entries: map[string]*entry[E]{}}
}

// Names returns the published names in sorted order.
func (m *Store[E]) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.entries))
	for name, record := range m.entries {
		if _, ok := record.Published(); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	return names
}

// Get returns the entry published under the name.
func (m *Store[E]) Get(name string) (E, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	record, ok := m.entries[name]
	if !ok {
		var zero E
		return zero, false
	}

	return record.Published()
}

// Update publishes a new entry under the name and retires the previous
// one.
//
// The callback is handed the current entry, when one exists, and returns
// the entry to store. Its error is returned unchanged and leaves the
// store as it was. On success the new entry atomically replaces the old
// one, then the parked entries are retried and the old one is freed or
// parked, so a callback must build a fresh entry rather than hand back the
// one it was given. A second mutation of the same name waits for this one
// to finish. Mutations of other names run at the same time.
func (m *Store[E]) Update(
	name string,
	publish func(current E, ok bool) (E, error),
) error {
	record, release := m.acquire(name)
	defer release()

	current, ok := record.Published()
	next, err := publish(current, ok)
	if err != nil {
		return err
	}

	record.Publish(next)

	m.deferMu.Lock()
	defer m.deferMu.Unlock()

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
// published. On success the entry is atomically removed, then the parked
// entries are retried and the entry is freed or parked.
//
// A concurrent mutation of the same name waits for this one to finish.
func (m *Store[E]) Delete(name string, unpublish func(current E) error) error {
	record, release := m.acquire(name)
	defer release()

	current, ok := record.Published()
	if !ok {
		return ErrNotFound
	}
	if err := unpublish(current); err != nil {
		return err
	}

	record.Unpublish()

	m.deferMu.Lock()
	defer m.deferMu.Unlock()

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
	m.deferMu.Lock()
	defer m.deferMu.Unlock()

	m.reclaimDeferred()
}

// acquire returns the record of the name locked, with the function that
// unlocks it.
//
// The function drops the record when nothing is published. Only a
// record's lock holder drops it, so a waiter that finds its record
// dropped retries.
func (m *Store[E]) acquire(name string) (*entry[E], func()) {
	for {
		record := m.lookupOrCreate(name)
		record.Lock()
		if !m.holds(name, record) {
			record.Unlock()
			continue
		}

		return record, func() {
			if _, ok := record.Published(); !ok {
				m.remove(name)
			}
			record.Unlock()
		}
	}
}

// lookupOrCreate returns the record of the name, creating a bare one when
// the name has none.
func (m *Store[E]) lookupOrCreate(name string) *entry[E] {
	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.entries[name]
	if !ok {
		record = &entry[E]{}
		m.entries[name] = record
	}

	return record
}

// holds reports whether the record is still the one stored under the
// name.
func (m *Store[E]) holds(name string, record *entry[E]) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.entries[name] == record
}

func (m *Store[E]) remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.entries, name)
}

// parkOrFree frees a retired entry and parks it when the free is
// refused. The caller must already hold the parked list's lock.
func (m *Store[E]) parkOrFree(entry E) {
	if err := entry.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, entry)
	}
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// already hold the parked list's lock.
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
