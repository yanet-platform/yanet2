package rib

import (
	"encoding/binary"
	"math/rand"
	"net/netip"
	"runtime"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
)

func TestMapTrieInsert(t *testing.T) {
	cases := []struct {
		prefix      string
		expectedIdx int
	}{
		{"192.168.9.1/16", 0},
		{"192.168.9.1/24", 1},
		{"192.168.18.0/8", 0},
	}
	mt := NewMapTrie(0)
	for _, c := range cases {
		prefix := netip.MustParsePrefix(c.prefix)
		expected := netip.MustParsePrefix(cases[c.expectedIdx].prefix).Masked()

		route := Route{
			MapTrieKey: MapTrieKey{Prefix: prefix.Masked()},
		}
		mt.InsertOrUpdate(route)
		addr := prefix.Addr()
		list, ok := mt.Lookup(addr)
		require.True(t, ok, "lookup %s, expected %s", addr, expected)
		actual := list.Routes[0].Prefix.Masked()
		require.Equal(t, expected, actual, "%s != %s", expected, actual)
	}
}

func TestMapTrieMatches(t *testing.T) {
	cases := []struct {
		prefix  string
		inMatch bool
	}{
		{"192.168.9.1/32", false},
		{"192.168.9.1/27", false},
		{"192.168.9.1/26", true},
		{"192.168.10.1/24", false},
		{"192.168.9.1/24", true},
		{"192.168.9.1/16", true},
		{"192.168.18.0/8", true},
		{"193.168.9.1/8", false},
		{"192.168.18.0/0", true},
	}
	mt := NewMapTrie(0)
	query := netip.MustParseAddr("192.168.9.32") // in /26 mask

	expected := []MapTrieKey{}
	for _, c := range cases {
		prefix := netip.MustParsePrefix(c.prefix)
		route := Route{MapTrieKey: MapTrieKey{Prefix: prefix.Masked()}}
		if c.inMatch {
			expected = append(expected, route.MapTrieKey)
		}
		t.Logf("expect matches: %s", expected)
		mt.InsertOrUpdate(route)
		actual := mt.Matches(query)
		require.Equal(t, expected, actual)
	}

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
		routes[idx] = Route{MapTrieKey: MapTrieKey{Prefix: p.Masked()}}
	}
	return addrs, routes
}

func TestMapTrieInsertMany(t *testing.T) {
	addrs, routes := initTestData(200000, 200000, true)
	mt := NewMapTrie(1024 * 4)
	for _, route := range routes {
		mt.InsertOrUpdate(route)
	}
	for _, addr := range addrs {
		_, ok := mt.Lookup(addr)
		require.True(t, ok, "lookup %s", addr)
	}
}

var benchDataInsertuniqAddrs, benchDataInsertuniqRoutes = initTestData(1_000_000, 400_000, false)

func Benchmark_mapTrie_insert_uniq(b *testing.B) {
	addrs := benchDataInsertuniqAddrs
	routes := benchDataInsertuniqRoutes

	inuse0 := heapInUse()
	mt := NewMapTrie(1024)
	inuse1 := heapInUse()
	b.Logf("The initial Memory usage of MapTrie: %s", datasize.ByteSize(inuse1-inuse0))
	b.ResetTimer()
	for range b.N {
		for idx := range routes {
			mt.InsertOrUpdate(routes[idx])
		}
	}
	b.StopTimer()
	inuse2 := heapInUse()
	var found int
	idx := max(rand.Intn(len(addrs))-1000, 0)
	for idx := range addrs[idx : idx+1000] {
		v, ok := mt.Lookup(addrs[idx])
		if !ok {
			panic("not found")
		}
		found += len(v.Routes)
	}
	b.Logf("Total number of routes %d: uniq %d", len(routes), mt.Len())
	b.Logf("Memory usage by mapTrie %s found=%d of 1k", datasize.ByteSize(inuse2-inuse0), found)
}

var benchDataInsertMessAddrs, benchDataInsertMessRoutes = initTestData(1_000_000, 400_000, true)

func Benchmark_mapTrie_insert_mess(b *testing.B) {
	routes := benchDataInsertMessRoutes
	inuse0 := heapInUse()
	mt := NewMapTrie(1024)
	inuse1 := heapInUse()
	b.Logf("Initial Memory usage by mapTrie %s", datasize.ByteSize(inuse1-inuse0))
	b.ResetTimer()
	for range b.N {
		for idx := range routes {
			mt.InsertOrUpdate(routes[idx])
		}
	}
	b.StopTimer()
	inuse2 := heapInUse()
	b.Logf("Total number of prefixes %d: uniq %d", len(routes), mt.Len())
	b.Logf("Memory usage by mapTrie %s", datasize.ByteSize(inuse2-inuse0))
}

var benchLookup1kAddrs, benchLookup1kRoutes = initTestData(1_000_000, 400_000, true)

func Benchmark_mapTrie_lookup_mess_1k(b *testing.B) {
	addrs := benchLookup1kAddrs
	routes := benchLookup1kRoutes

	mt := NewMapTrie(1024)
	for _, route := range routes {
		mt.InsertOrUpdate(route)
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
