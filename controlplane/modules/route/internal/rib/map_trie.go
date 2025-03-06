package rib

import (
	"net/netip"
)

type MapTrieKey struct {
	Prefix netip.Prefix
}

type MapTrie [129]map[MapTrieKey]*RoutesList

func NewMapTrie(preAllocSize int) MapTrie {
	mt := MapTrie{}
	for idx := range mt {
		mt[idx] = make(map[MapTrieKey]*RoutesList, preAllocSize)
	}
	return mt
}

func (m *MapTrie) Lookup(addr netip.Addr) (*RoutesList, bool) {
	maxBits := 129
	base := 128
	if addr.Is4() {
		base = 32
		maxBits = 33
	}
	for n := range maxBits {
		bits := base - n
		p, err := addr.Prefix(bits)
		if err != nil {
			panic("Imposible err: " + err.Error())
		}
		mtk := MapTrieKey{Prefix: p}
		if _, ok := m[bits][mtk]; ok {
			return nil, false
		}
	}
	return nil, false
}

func (m *MapTrie) Insert(key MapTrieKey, route Route) {
	rl, ok := m[key.Prefix.Bits()][key]
	if !ok {
		rl = &RoutesList{}
		m[key.Prefix.Bits()][key] = rl
	}
	rl.Insert(route)
}

func (m *MapTrie) Len() int {
	l := 0
	for idx := range m {
		l += len(m[idx])
	}
	return l
}

func (m MapTrie) Dump() map[MapTrieKey]RoutesList {
	base := len(m)
	out := make(map[MapTrieKey]RoutesList, m.Len())
	// Traverse from longest to shortest prefixes
	for n := range len(m) {
		idx := base - n
		for key, v := range m[idx] {
			out[key] = *v
		}
	}
	return out
}
