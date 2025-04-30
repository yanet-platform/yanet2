package discovery

import (
	"iter"
	"maps"
	"sync"
)

// Cache is a generic netlink key-value cache.
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
	// The returned type provides only read-only access, while this structure
	// only allows to atomically swap the entire table.
	return CacheView[K, V]{cache: m.cache}
}

// Swap atomically swaps the entire cache.
func (m *Cache[K, V]) Swap(cache map[K]V) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache = cache
}

// CacheView is a read-only view of the cache.
type CacheView[K comparable, V any] struct {
	cache map[K]V
}

// Lookup returns the value for the specified key.
func (m *CacheView[K, V]) Lookup(key K) (V, bool) {
	v, ok := m.cache[key]
	return v, ok
}

// Entries returns entries in the cache as an iterator.
func (m *CacheView[K, V]) Entries() (iter.Seq[V], int) {
	return maps.Values(m.cache), len(m.cache)
}
