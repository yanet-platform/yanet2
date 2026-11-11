package rcucache

import (
	"iter"
	"maps"
	"sync/atomic"
)

// Cache is a generic key-value cache with lock-free reads.
//
// A map handed to the cache is published and must not be modified again:
// readers keep a consistent snapshot for as long as they hold it, and every
// change publishes a whole new map. Writers that build the next map from
// the current one must serialize among themselves, or concurrent changes
// are lost. The zero value is an empty cache.
type Cache[K comparable, V any] struct {
	cache atomic.Pointer[map[K]V]
}

// NewCache constructs a new cache using specified underlying map.
func NewCache[K comparable, V any](cache map[K]V) *Cache[K, V] {
	m := &Cache[K, V]{}
	m.Swap(cache)
	return m
}

// NewEmptyCache returns an empty cache.
func NewEmptyCache[K comparable, V any]() *Cache[K, V] {
	return NewCache(map[K]V{})
}

// View returns a read-only snapshot of this cache, that can be used
// concurrently to look up cached entries.
func (m *Cache[K, V]) View() CacheView[K, V] {
	cache := m.cache.Load()
	if cache == nil {
		return CacheView[K, V]{}
	}
	return CacheView[K, V]{cache: *cache}
}

// Swap atomically swaps the entire cache.
func (m *Cache[K, V]) Swap(cache map[K]V) {
	m.cache.Store(&cache)
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
func (m CacheView[K, V]) Entries() iter.Seq[V] {
	return maps.Values(m.cache)
}

// Iter returns the cache entries as a key-value iterator.
func (m CacheView[K, V]) Iter() iter.Seq2[K, V] {
	return maps.All(m.cache)
}

// Len returns the number of cache entries.
func (m CacheView[K, V]) Len() int {
	return len(m.cache)
}
