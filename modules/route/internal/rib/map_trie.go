package rib

import (
	"maps"
)

// MapTrieKey defines requirements for keys used in the MapTrie data structure.
//
// The type parameter T represents the concrete type implementing this
// interface.
type MapTrieKey[T any] interface {
	// comparable ensures that keys can be used in maps.
	comparable
	// Masked returns a normalized version of the key with only significant
	// bits.
	//
	// This ensures proper prefix matching behavior.
	Masked() T
	// Bits returns the number of significant bits in this key.
	//
	// For IP prefixes, this would be the prefix length.
	Bits() int
}

// MapTrieQuery defines the interface for objects that can be used for querying
// the MapTrie.
type MapTrieQuery[K MapTrieKey[K]] interface {
	// BitLen returns the maximum number of significant bits in this query.
	//
	// For IP addresses, this would be 32 for IPv4 or 128 for IPv6.
	BitLen() int
	// Prefix generates a key of the specified bit length from this query.
	//
	// For IP addresses, this would create a network prefix of the given
	// length.
	// Returns an error if the requested bit length is invalid.
	Prefix(int) (K, error)
}

// MapTrie is a generic data structure with properties of a prefix trie but
// implemented using maps.
//
// It is an array of maps, where each index corresponds to a prefix length.
//
// The maximum size is 129 to accommodate both IPv4 (32 bits) and IPv6 (128 bits),
// plus an extra slot for the default route (/0).
//
// The type parameter K represents the key type that implements MapTrieKey.
// The type parameter Q represents the query type that implements MapTrieQuery.
// The type parameter V represents the value type stored for each prefix.
type MapTrie[K MapTrieKey[K], Q MapTrieQuery[K], V any] [129]map[K]V

// NewMapTrie returns a new MapTrie data structure with the specified
// initial capacity.
func NewMapTrie[K MapTrieKey[K], Q MapTrieQuery[K], V any](cap int) MapTrie[K, Q, V] {
	trie := MapTrie[K, Q, V]{}

	for idx := range trie {
		trie[idx] = make(map[K]V, cap)
	}

	return trie
}

// Lookup searches the MapTrie for a value that matches the longest
// possible prefix for the given query.
//
// If no match is found, the function returns the zero value and false.
func (m *MapTrie[K, Q, V]) Lookup(query Q) (K, V, bool) {
	bitLen := query.BitLen()

	for bits := bitLen; bits >= 0; bits-- {
		prefix, _ := query.Prefix(bits)

		if value, ok := m[bits][prefix]; ok {
			return prefix, value, true
		}
	}

	var zeroPrefix K
	var zeroValue V
	return zeroPrefix, zeroValue, false
}

// LookupTraverse finds all prefixes in the MapTrie that contain the given query,
// calling a function for each matching prefix in ascending order of prefix length.
//
// The callback function receives each matching prefix and its associated value,
// and can control traversal by returning a boolean value. Return true to continue
// processing, or false to stop the traversal.
func (m *MapTrie[K, Q, V]) LookupTraverse(query Q, fn func(K, V) bool) {
	bitLen := query.BitLen()

	// Note, that "<=" is not a bug!
	for bits := 0; bits <= bitLen; bits++ {
		prefix, _ := query.Prefix(bits)

		if value, ok := m[bits][prefix]; ok {
			if fn(prefix, value) {
				continue
			}
		}
	}
}

// LookupTraverseRev finds all prefixes in the MapTrie that contain the given query,
// calling a function for each matching prefix in descending order of prefix length.
//
// The callback function receives each matching prefix and its associated value,
// and can control traversal by returning a boolean value. Return true to continue
// processing, or false to stop the traversal.
func (m *MapTrie[K, Q, V]) LookupTraverseRev(query Q, fn func(K, V) bool) {
	bitLen := query.BitLen()

	for bits := bitLen; bits >= 0; bits-- {
		prefix, _ := query.Prefix(bits)

		if value, ok := m[bits][prefix]; ok {
			if fn(prefix, value) {
				continue
			}
		}
	}
}

// Matches returns a list of keys that match the given query.
//
// The returned slice is sorted from the longest to the shortest prefix.
// It returns an empty list if there are no matches.
func (m *MapTrie[K, Q, V]) Matches(query Q) []K {
	matches := []K{}

	m.LookupTraverseRev(query, func(prefix K, value V) bool {
		matches = append(matches, prefix)
		return true
	})

	return matches
}

// InsertOrUpdate adds a new entry or updates an existing one in the MapTrie.
//
// The function first normalizes the prefix with masking, then either inserts a new
// value using the onEmpty callback or updates an existing value using the onUpdate
// callback.
func (m *MapTrie[K, Q, V]) InsertOrUpdate(prefix K, onEmpty func() V, onUpdate func(V) V) {
	prefix = prefix.Masked()
	bits := prefix.Bits()

	if currValue, ok := m[bits][prefix]; ok {
		m[bits][prefix] = onUpdate(currValue)
		return
	}

	m[bits][prefix] = onEmpty()
}

// Len returns the total number of prefixes stored in the MapTrie.
//
// This counts entries across all prefix lengths.
func (m *MapTrie[K, Q, V]) Len() int {
	l := 0
	for idx := range m {
		l += len(m[idx])
	}

	return l
}

// UpdateOrDelete updates existing entry and deletes it from the MapTrie if update
// indicates that updated entry becomes empty.
func (m *MapTrie[K, Q, V]) UpdateOrDelete(prefix K, update func(V) (V, bool)) {
	prefix = prefix.Masked()
	bits := prefix.Bits()

	if value, ok := m[bits][prefix]; ok {
		if newValue, zero := update(value); zero {
			delete(m[bits], prefix)
		} else {
			m[bits][prefix] = newValue
		}
	}
}

// Dump creates a flat map containing all prefixes and their values from the MapTrie.
func (m MapTrie[K, Q, V]) Dump() map[K]V {
	out := make(map[K]V, m.Len())

	// Traverse from longest to shortest prefixes.
	for idx := len(m) - 1; idx >= 0; idx-- {
		maps.Copy(out, m[idx])
	}

	return out
}

// Clone returns a copy of m.  This is a shallow clone:
// the new keys and values are set using ordinary assignment.
func (m MapTrie[K, Q, V]) Clone() MapTrie[K, Q, V] {
	out := MapTrie[K, Q, V]{}

	for idx := range out {
		out[idx] = maps.Clone(m[idx])
	}
	return out
}
