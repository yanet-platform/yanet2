package rcucache

import (
	"iter"
	"maps"
	"sync"
)

// Cache is a generic key-value cache.
type Cache[K comparable, V any] struct {
	mu    sync.RWMutex
	cache map[K]V
}

// NewCache constructs a new cache using specified underlying map.
func NewCache[K comparable, V any](cache map[K]V) *Cache[K, V] {
	return &Cache[K, V]{
		cache: cache,
	}
}

// NewEmptyCache returns an empty cache.
func NewEmptyCache[K comparable, V any]() *Cache[K, V] {
	return NewCache(map[K]V{})
}

// View returns a read-only view of this cache, that can be used concurrently
// to lookup cached	entries.
func (m *Cache[K, V]) View() CacheView[K, V] {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Just copy the pointer here.
	//
	// The view reads the table without taking a lock, so it stays sound
	// only while nothing writes into that table in place; swapping a
	// rebuilt table in leaves the view reading the one it was given.
	return CacheView[K, V]{cache: m.cache}
}

// Swap atomically swaps the entire cache.
//
// The supplied table becomes the published one, so a caller that keeps a
// reference and writes through it later corrupts a reader already walking
// it: hand over a table nothing else holds.
func (m *Cache[K, V]) Swap(cache map[K]V) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache = cache
}

// Set inserts or updates a single entry in the cache.
//
// The entry lands in the table every outstanding view is already reading,
// and a reader walking that table at the same time aborts the process.
// Use this only before the cache is reachable by a reader; afterwards
// swap a rebuilt table in instead.
func (m *Cache[K, V]) Set(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache[key] = value
}

// Delete removes a single entry from the cache.
//
// Carries the same hazard as Set: it writes into the table outstanding
// views are reading.
func (m *Cache[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.cache, key)
}

// CacheView is a read-only view of the cache.
type CacheView[K comparable, V any] struct {
	cache map[K]V
}

// Lookup returns the value for the specified key.
func (m CacheView[K, V]) Lookup(key K) (V, bool) {
	v, ok := m.cache[key]
	return v, ok
}

// Entries returns entries in the cache as an iterator.
func (m CacheView[K, V]) Entries() (iter.Seq[V], int) {
	return maps.Values(m.cache), len(m.cache)
}

// All returns the cache entries as a key-value iterator.
func (m CacheView[K, V]) All() (iter.Seq2[K, V], int) {
	return maps.All(m.cache), len(m.cache)
}
