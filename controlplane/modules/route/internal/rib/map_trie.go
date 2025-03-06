package rib

import (
	"net/netip"
)

type MapTrieKey struct {
	Prefix netip.Prefix
}

type MapTrie [129]map[MapTrieKey]*RoutesList

func NewMapTrie(capacity int) MapTrie {
	mt := MapTrie{}
	for idx := range mt {
		mt[idx] = make(map[MapTrieKey]*RoutesList, capacity)
	}
	return mt
}

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

func (m *MapTrie) Entry(route Route) *RoutesList {
	rl, ok := m[route.Prefix.Bits()][route.MapTrieKey]
	if !ok {
		rl = &RoutesList{}
		m[route.Prefix.Bits()][route.MapTrieKey] = rl
	}
	return rl
}

func (m *MapTrie) InsertOrUpdate(route Route) {
	m.Entry(route).Insert(route)
}

func (m *MapTrie) Len() int {
	l := 0
	for idx := range m {
		l += len(m[idx])
	}
	return l
}

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
