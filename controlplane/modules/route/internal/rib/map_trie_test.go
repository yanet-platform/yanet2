package rib

import (
	"encoding/binary"
	"math/rand"
	"net/netip"
	"runtime"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var onEmpty = func(v int) func() int {
	return func() int { return v }
}

var onUpdate = func(v int) func(int) int {
	return func(int) int { return v }
}

func Test_MapTrie_LookupEmpty(t *testing.T) {
	trie := NewMapTrie[netip.Prefix, netip.Addr, int](0)

	// Expect failed lookup in empty trie.
	_, ok := trie.Lookup(netip.MustParseAddr("192.168.9.1"))
	assert.False(t, ok)
}

func Test_MapTrie_LookupAfterInsert(t *testing.T) {
	cases := []struct {
		addr        string
		expectedOk  bool
		expectedIdx int
	}{
		{"192.168.9.1", true, 0},
		{"127.0.0.1", false, 0},
	}

	trie := NewMapTrie[netip.Prefix, netip.Addr, int](0)
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.0.0/16"), onEmpty(0), onUpdate(0))

	for _, c := range cases {
		v, ok := trie.Lookup(netip.MustParseAddr(c.addr))
		require.Equal(t, c.expectedOk, ok)
		assert.Equal(t, c.expectedIdx, v)
	}
}

func Test_MapTrie_LookupAfterInsertUpdate(t *testing.T) {
	cases := []struct {
		addr        string
		expectedOk  bool
		expectedIdx int
	}{
		{"192.168.9.1", true, 1},
		{"127.0.0.1", false, 0},
	}

	trie := NewMapTrie[netip.Prefix, netip.Addr, int](0)
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.0.0/16"), onEmpty(0), onUpdate(0))
	// This should update the value to 1.
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.0.0/16"), onEmpty(1), onUpdate(1))

	for _, c := range cases {
		v, ok := trie.Lookup(netip.MustParseAddr(c.addr))
		require.Equal(t, c.expectedOk, ok)
		assert.Equal(t, c.expectedIdx, v)
	}
}

func Test_MapTrie_LookupAfterInsertNestedPrefixes(t *testing.T) {
	cases := []struct {
		addr        string
		expectedOk  bool
		expectedIdx int
	}{
		{"192.168.1.1", true, 4},
		{"192.168.1.2", true, 3},
		{"192.168.2.2", true, 2},
		{"192.200.1.1", true, 1},
		{"127.0.0.1", true, 0},
	}

	trie := NewMapTrie[netip.Prefix, netip.Addr, int](0)
	trie.InsertOrUpdate(netip.MustParsePrefix("0.0.0.0/0"), onEmpty(0), onUpdate(0))
	trie.InsertOrUpdate(netip.MustParsePrefix("192.0.0.0/8"), onEmpty(1), onUpdate(1))
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.0.0/16"), onEmpty(2), onUpdate(2))
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.1.0/24"), onEmpty(3), onUpdate(3))
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.1.1/32"), onEmpty(4), onUpdate(4))

	for _, c := range cases {
		v, ok := trie.Lookup(netip.MustParseAddr(c.addr))
		require.Equal(t, c.expectedOk, ok)
		assert.Equal(t, c.expectedIdx, v)
	}
}

func Test_MapTrie_Lookup6(t *testing.T) {
	cases := []struct {
		prefix      string
		expectedOk  bool
		expectedIdx int
	}{
		{"fd25:cf19:6b13:cafe:babe:be57:f00d:04a5/128", false, 0},
		{"fd25:cf19:6b13:cafe:babe:be57:f00d:400/120", false, 0},
		{"fd25:cf19:6b13:cafe:babe:be57:f00d::/112", true, 2},
		{"fd25:cf19:6b13:cafe::/64", true, 2},
		{"fd25:8888:6b13:cafe::/64", true, 2},
		{"fd25::/16", true, 2},
	}

	addr := netip.MustParseAddr("fd25:cf19:6b13:cafe:babe:be57:f00d:0001")

	trie := NewMapTrie[netip.Prefix, netip.Addr, int](0)

	for idx, c := range cases {
		prefix := netip.MustParsePrefix(c.prefix).Masked()

		trie.InsertOrUpdate(prefix, onEmpty(idx), onUpdate(idx))

		value, ok := trie.Lookup(addr)
		require.Equal(t, c.expectedOk, ok,
			"lookup expected match==%t, but ok=%t, prefix=%s", c.expectedOk, ok, prefix)
		require.Equal(t, c.expectedIdx, value,
			"lookup expected value==%d, but value=%d, prefix=%s", c.expectedIdx, value, prefix)
	}
}

func Test_MapTrie_Lookup6TopDownInsert(t *testing.T) {
	cases := []struct {
		prefix      string
		expectedOk  bool
		expectedIdx int
	}{
		{"fd25::/16", true, 0},
		{"fd25:8888:6b13:cafe::/64", true, 0},
		{"fd25:cf19:6b13:cafe::/64", true, 2},
		{"fd25:cf19:6b13:cafe:babe:be57:f00d::/112", true, 3},
		{"fd25:cf19:6b13:cafe:babe:be57:f00d:400/120", true, 3},
		{"fd25:cf19:6b13:cafe:babe:be57:f00d:04a5/128", true, 3},
		{"fd25:cf19:6b13:cafe:babe:be57:f00d:0001/128", true, 6},
	}

	addr := netip.MustParseAddr("fd25:cf19:6b13:cafe:babe:be57:f00d:0001")

	trie := NewMapTrie[netip.Prefix, netip.Addr, int](0)

	for idx, c := range cases {
		prefix := netip.MustParsePrefix(c.prefix).Masked()

		trie.InsertOrUpdate(prefix, onEmpty(idx), onUpdate(idx))

		value, ok := trie.Lookup(addr)
		require.Equal(t, c.expectedOk, ok,
			"lookup expected match==%t, but ok=%t, prefix=%s", c.expectedOk, ok, prefix)
		require.Equal(t, c.expectedIdx, value,
			"lookup expected value==%d, but value=%d, prefix=%s", c.expectedIdx, value, prefix)
	}
}

func Test_MapTrie_LookupTraverse(t *testing.T) {
	trie := NewMapTrie[netip.Prefix, netip.Addr, int](0)

	traverseLPM := func(addr netip.Addr) []netip.Prefix {
		out := make([]netip.Prefix, 0)
		trie.LookupTraverse(addr, func(prefix netip.Prefix, value int) bool {
			out = append(out, prefix)
			return true
		})

		return out
	}

	addr := netip.MustParseAddr("192.168.9.32")
	assert.Equal(t, []netip.Prefix{}, traverseLPM(addr))

	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.9.32/32"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.9.0/24"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	// Note, that 192.168.9.0/27 does not contain 192.168.9.32 ...
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.9.0/27"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	// ... but 192.168.9.0/26 does.
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.9.0/26"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	// Does not affect.
	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.10.0/24"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	trie.InsertOrUpdate(netip.MustParsePrefix("192.168.0.0/16"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	// 192.168.0.0 in hex.
	trie.InsertOrUpdate(netip.MustParsePrefix("a8c0::/16"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	trie.InsertOrUpdate(netip.MustParsePrefix("a8c0::/112"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	// v6 mapped ::ffff:168.192.1.9
	trie.InsertOrUpdate(netip.MustParsePrefix("::ffff:a8c0:109/16"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	// v6 mapped ::ffff:168.192.1.9
	trie.InsertOrUpdate(netip.MustParsePrefix("::ffff:a8c0:109/112"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	trie.InsertOrUpdate(netip.MustParsePrefix("192.0.0.0/8"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.0.0.0/8"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	trie.InsertOrUpdate(netip.MustParsePrefix("193.168.9.1/8"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.0.0.0/8"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	// NOTE: this is very important case! No intermix between IPv4 and IPv6 ...
	trie.InsertOrUpdate(netip.MustParsePrefix("::/0"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("192.0.0.0/8"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))

	// ... but IPv4 UNSPECIFIED is okay.
	trie.InsertOrUpdate(netip.MustParsePrefix("0.0.0.0/0"), onEmpty(0), onUpdate(0))
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/0"),
		netip.MustParsePrefix("192.0.0.0/8"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("192.168.9.0/24"),
		netip.MustParsePrefix("192.168.9.0/26"),
		netip.MustParsePrefix("192.168.9.32/32"),
	}, traverseLPM(addr))
}

func Fuzz_MapTrie_InsertAndLookup(f *testing.F) {
	addr := netip.MustParseAddr("fd25:c819:6888:0:b282:ffff:1841:3832").As16()
	allZero := netip.IPv6Unspecified().As16()
	allFF := netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff").As16()

	f.Add(byte(120), allZero[:], addr[:])
	f.Add(byte(30), addr[:], allFF[:])
	f.Add(byte(0), addr[:], addr[:])
	f.Add(byte(100), allZero[:], allFF[:])
	f.Add(byte(128), allFF[:], allFF[:])

	f.Fuzz(func(t *testing.T, m byte, pb []byte, qb []byte) {
		mt := NewMapTrie[netip.Prefix, netip.Addr, RoutesList](0)

		prefixBytes := [16]byte{}
		copy(prefixBytes[:], pb)
		prefixAddr := netip.AddrFrom16(prefixBytes)

		m = min(m, 128)
		p := netip.PrefixFrom(prefixAddr, int(m)).Masked()

		route := Route{Prefix: p}
		mt.InsertOrUpdate(
			p,
			func() RoutesList {
				return RoutesList{
					Routes: []Route{route},
				}
			},
			func(m RoutesList) RoutesList {
				m.Routes = append(m.Routes, route)
				return m
			},
		)

		queryBytes := [16]byte{}
		copy(queryBytes[:], qb)
		queryAddr := netip.AddrFrom16(queryBytes)

		_, ok := mt.Lookup(queryAddr)
		qaPrefix := netip.PrefixFrom(queryAddr, int(m)).Masked()
		equal := p == qaPrefix

		switch [2]bool{ok, equal} {
		case [2]bool{false, true}:
			t.Errorf("query addr %s should match %s", queryAddr, p)
		case [2]bool{true, false}:
			t.Errorf("unexpected match of addr %s by prefix %s", qaPrefix, p)
		}
		matches := mt.Matches(queryAddr)
		matched := len(matches) > 0
		switch [2]bool{ok && equal, matched} {
		case [2]bool{false, true}:
			t.Errorf("unexpected return from Matches: %s, prefix=%s, queryAddr=%s", matches, p, queryAddr)
		case [2]bool{true, false}:
			t.Errorf("Matches should return a match: prefix=%s, queryAddr=%s", p, queryAddr)
		}
	})
}

func heapInUse() uint64 {
	runtime.GC()
	ms := runtime.MemStats{}
	runtime.ReadMemStats(&ms)

	return ms.HeapInuse
}

func initTestData(v4count int, v6count int, random bool) ([]netip.Addr, []Route) {
	maskShift := 8
	addrs := make([]netip.Addr, 0, v4count+v6count)
	for idx := range v4count {
		v4a := [4]byte{}
		a := uint32(idx)
		a <<= uint32(maskShift)
		if random {
			a = rand.Uint32()
		}
		binary.BigEndian.PutUint32(v4a[:], a)
		addrs = append(addrs, netip.AddrFrom4(v4a))
	}

	for idx := range v6count {
		v6a := [16]byte{}
		a := uint64(0xfe80dada00b0feca)
		b := uint64(idx)
		b <<= uint64(maskShift)
		if random {
			b = rand.Uint64()

		}
		binary.BigEndian.PutUint64(v6a[:], a)
		binary.BigEndian.PutUint64(v6a[8:], b)
		addrs = append(addrs, netip.AddrFrom16(v6a))
	}

	routes := make([]Route, len(addrs))
	for idx, a := range addrs {
		mask := a.BitLen() - maskShift
		if random {
			mask = rand.Intn(a.BitLen() + 1)
		}
		p, _ := a.Prefix(mask)
		routes[idx] = Route{Prefix: p.Masked()}
	}
	return addrs, routes
}

func Test_MapTrie_InsertMany(t *testing.T) {
	addrs, routes := initTestData(200000, 200000, true)
	mt := NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024 * 4)
	for _, route := range routes {
		mt.InsertOrUpdate(
			route.Prefix,
			func() RoutesList { return RoutesList{Routes: []Route{route}} },
			func(m RoutesList) RoutesList {
				m.Routes = append(m.Routes, route)
				return m
			},
		)
	}
	for _, addr := range addrs {
		_, ok := mt.Lookup(addr)
		require.True(t, ok, "lookup %s", addr)
	}
}

var benchDataInsertuniqAddrs, benchDataInsertuniqRoutes = initTestData(1_000_000, 400_000, false)

func Benchmark_MapTrie_InsertUniq(b *testing.B) {
	addrs := benchDataInsertuniqAddrs
	routes := benchDataInsertuniqRoutes

	inuse0 := heapInUse()
	trie := NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024)
	inuse1 := heapInUse()
	b.Logf("The initial Memory usage of MapTrie: %s", datasize.ByteSize(inuse1-inuse0))

	b.ResetTimer()
	for range b.N {
		for idx, route := range routes {
			trie.InsertOrUpdate(
				routes[idx].Prefix,
				func() RoutesList {
					return RoutesList{Routes: []Route{route}}
				},
				func(m RoutesList) RoutesList {
					m.Routes = append(m.Routes, route)
					return m
				},
			)
		}
	}
	b.StopTimer()
	inuse2 := heapInUse()
	var found int
	idx := max(rand.Intn(len(addrs))-1000, 0)
	for idx := range addrs[idx : idx+1000] {
		v, ok := trie.Lookup(addrs[idx])
		if !ok {
			panic("not found")
		}
		found += len(v.Routes)
	}
	b.Logf("Total number of routes %d: uniq %d", len(routes), trie.Len())
	b.Logf("Memory usage by mapTrie %s found=%d of 1k", datasize.ByteSize(inuse2-inuse0), found)
}

var _, benchDataInsertMessRoutes = initTestData(1_000_000, 400_000, true)

func Benchmark_MapTrie_InsertMess(b *testing.B) {
	routes := benchDataInsertMessRoutes
	inUse0 := heapInUse()
	trie := NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024)

	inUse1 := heapInUse()
	b.Logf("Initial Memory usage by mapTrie %s", datasize.ByteSize(inUse1-inUse0))
	b.ResetTimer()
	for range b.N {
		for idx, route := range routes {
			trie.InsertOrUpdate(
				routes[idx].Prefix,
				func() RoutesList {
					return RoutesList{Routes: []Route{route}}
				},
				func(m RoutesList) RoutesList {
					m.Routes = append(m.Routes, route)
					return m
				},
			)
		}
	}
	b.StopTimer()
	inuse2 := heapInUse()

	b.Logf("Total number of prefixes %d: uniq %d", len(routes), trie.Len())
	b.Logf("Memory usage by mapTrie %s", datasize.ByteSize(inuse2-inUse0))
}

var benchLookup1kAddrs, benchLookup1kRoutes = initTestData(1_000_000, 400_000, true)

func Benchmark_mapTrie_lookup_mess_1k(b *testing.B) {
	addrs := benchLookup1kAddrs
	routes := benchLookup1kRoutes

	mt := NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024)
	for _, route := range routes {
		mt.InsertOrUpdate(
			route.Prefix,
			func() RoutesList {
				return RoutesList{Routes: []Route{route}}
			},
			func(m RoutesList) RoutesList {
				return m
			},
		)
	}

	var found int
	b.ResetTimer()
	for idx := range b.N {
		v, ok := mt.Lookup(addrs[idx%len(addrs)])
		if !ok {
			panic("not found")
		}
		found += len(v.Routes)
	}
	b.StopTimer()

	b.Logf("Total number of prefixes %d: uniq: %d, found: %d", len(routes), mt.Len(), found)
}
