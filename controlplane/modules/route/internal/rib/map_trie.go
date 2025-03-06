package rib

import (
	"net/netip"
)

type MapTrieKey struct {
	Prefix netip.Prefix
}

// MapTrie has the properties of a prefix trie but stores prefixes in maps.
type MapTrie [129]map[MapTrieKey]*RoutesList

// NewMapTrie returns a new MapTrie data structure.
func NewMapTrie(capacity int) MapTrie {
	mt := MapTrie{}
	for idx := range mt {
		mt[idx] = make(map[MapTrieKey]*RoutesList, capacity)
	}
	return mt
}

// Lookup searches the MapTrie for a RoutesList that matches the longest
// possible prefix.
// If no match is found, the function returns nil.
func (m *MapTrie) Lookup(addr netip.Addr) (*RoutesList, bool) {
	bitLen := addr.BitLen()
	for bits := bitLen; bits >= 0; bits-- {
		p, _ := addr.Prefix(bits)
		mtk := MapTrieKey{Prefix: p}
		if v, ok := m[bits][mtk]; ok {
			return v, true
		}
	}
	return nil, false
}

// Matches returns a list of keys that match the given IP.
// The returned slice is sorted from the longest to the shortest prefix.
// It returns an empty list if there are no matches.
func (m *MapTrie) Matches(addr netip.Addr) []MapTrieKey {
	bitLen := addr.BitLen()
	matches := []MapTrieKey{}
	for bits := bitLen; bits >= 0; bits-- {
		p, _ := addr.Prefix(bits)
		mtk := MapTrieKey{Prefix: p}
		if _, ok := m[bits][mtk]; ok {
			matches = append(matches, mtk)
		}
	}
	return matches
}

// Entry returns a reference to the RoutesList for insertion.
// The returned RoutesList should not be preserved by the user.
// If there is no RoutesList at the given key, Entry will create it
// and return a reference to the newly created RoutesList.
func (m *MapTrie) Entry(route Route) *RoutesList {
	rl, ok := m[route.Prefix.Bits()][route.MapTrieKey]
	if !ok {
		rl = &RoutesList{}
		m[route.Prefix.Bits()][route.MapTrieKey] = rl
	}
	return rl
}

// InsertOrUpdate inserts the Route into the existing RoutesList or creates
// a new RoutesList with the given Route.
func (m *MapTrie) InsertOrUpdate(route Route) {
	m.Entry(route).Insert(route)
}

// Len returns the total number of routes stored in the trie
func (m *MapTrie) Len() int {
	l := 0
	for idx := range m {
		l += len(m[idx])
	}
	return l
}

// Dump creates a copy of the data stored in the trie and returns it.
func (m MapTrie) Dump() map[MapTrieKey]RoutesList {
	out := make(map[MapTrieKey]RoutesList, m.Len())
	// Traverse from longest to shortest prefixes
	for idx := len(m) - 1; idx >= 0; idx-- {
		for key, v := range m[idx] {
			out[key] = *v
		}
	}
	return out
}
