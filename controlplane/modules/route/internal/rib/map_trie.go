package rib

import (
	"net/netip"
)

// MapTrie is a generic data structure with properties of a prefix trie but
// implemented using maps.
//
// It is an array of maps, where each index corresponds to a prefix length.
//
// The maximum size is 129 to accommodate both IPv4 (32 bits) and IPv6 (128 bits),
// plus an extra slot for the default route (/0).
//
// The type parameter V represents the value type stored for each prefix.
type MapTrie[V any] [129]map[netip.Prefix]V

// NewMapTrie returns a new MapTrie data structure with the specified
// initial capacity.
func NewMapTrie[V any](cap int) MapTrie[V] {
	trie := MapTrie[V]{}

	for idx := range trie {
		trie[idx] = make(map[netip.Prefix]V, cap)
	}

	return trie
}

// Lookup searches the MapTrie for a value that matches the longest
// possible prefix for the given IP address.
//
// If no match is found, the function returns the zero value and false.
func (m *MapTrie[V]) Lookup(addr netip.Addr) (V, bool) {
	bitLen := addr.BitLen()

	for bits := bitLen; bits >= 0; bits-- {
		// TODO: leave comment why we ignore error.
		prefix, _ := addr.Prefix(bits)

		if value, ok := m[bits][prefix]; ok {
			return value, true
		}
	}

	var zero V
	return zero, false
}

// LookupTraverse finds all prefixes in the MapTrie that contain the given IP address,
// calling a function for each matching prefix in ascending order of prefix length.
//
// The callback function receives each matching prefix and its associated value,
// and can control traversal by returning a boolean value. Return true to continue
// processing, or false to stop the traversal.
func (m *MapTrie[V]) LookupTraverse(addr netip.Addr, fn func(netip.Prefix, V) bool) {
	bitLen := addr.BitLen()

	// Note, that "<=" is not a bug!
	for bits := 0; bits <= bitLen; bits++ {
		prefix, _ := addr.Prefix(bits)

		if value, ok := m[bits][prefix]; ok {
			if fn(prefix, value) {
				continue
			}
		}
	}
}

func (m *MapTrie[V]) LookupTraverseRev(addr netip.Addr, fn func(netip.Prefix, V) bool) {
	bitLen := addr.BitLen()

	for bits := bitLen; bits >= 0; bits-- {
		prefix, _ := addr.Prefix(bits)

		if value, ok := m[bits][prefix]; ok {
			if fn(prefix, value) {
				continue
			}
		}
	}
}

// Matches returns a list of keys that match the given IP.
//
// The returned slice is sorted from the longest to the shortest prefix.
// It returns an empty list if there are no matches.
func (m *MapTrie[V]) Matches(addr netip.Addr) []netip.Prefix {
	matches := []netip.Prefix{}

	m.LookupTraverseRev(addr, func(prefix netip.Prefix, value V) bool {
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
func (m *MapTrie[V]) InsertOrUpdate(prefix netip.Prefix, onEmpty func() V, onUpdate func(V) V) {
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
func (m *MapTrie[V]) Len() int {
	l := 0
	for idx := range m {
		l += len(m[idx])
	}

	return l
}

// Dump creates a flat map containing all prefixes and their values from the MapTrie.
//
// The function traverses the trie from longest to shortest prefixes, which means
// that if there are overlapping prefixes, the shorter ones will overwrite the longer
// ones in the returned map.
func (m MapTrie[V]) Dump() map[netip.Prefix]V {
	out := make(map[netip.Prefix]V, m.Len())

	// Traverse from longest to shortest prefixes.
	for idx := len(m) - 1; idx >= 0; idx-- {
		for key, v := range m[idx] {
			out[key] = v
		}
	}

	return out
}
