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

func initTestData(v4count int, v6count int, random bool) []netip.Addr {
	out := make([]netip.Addr, 0, v4count+v6count)
	for idx := range v4count {
		v4a := [4]byte{}
		a := uint32(idx)
		if random {
			a = rand.Uint32()
		}
		binary.BigEndian.PutUint32(v4a[:], a)
		out = append(out, netip.AddrFrom4(v4a))
	}

	for idx := range v6count {
		v6a := [16]byte{}
		a := uint64(0xfe80dada00b0feca)
		b := uint64(idx)
		if random {
			b = rand.Uint64()

		}
		binary.BigEndian.PutUint64(v6a[:], a)
		binary.BigEndian.PutUint64(v6a[8:], b)
		out = append(out, netip.AddrFrom16(v6a))
	}

	return out
}

func TestMapTrieInsertMany(t *testing.T) {
	out := initTestData(200000, 200000, true)
	mt := NewMapTrie(1024)
	for _, addr := range out {
		p, err := addr.Prefix(rand.Intn(addr.BitLen() + 1))
		require.NoError(t, err)
		r := Route{MapTrieKey: MapTrieKey{Prefix: p.Masked()}}
		mt.InsertOrUpdate(r)
	}
	for _, addr := range out {
		_, ok := mt.Lookup(addr)
		require.True(t, ok, "lookup %s", addr)
	}
}

var benchDataInsertuniq = initTestData(1_000_000, 400_000, false)

func Benchmark_mapTrie_insert_uniq(b *testing.B) {
	addrs := benchDataInsertuniq
	routes := make([]Route, len(addrs))
	for idx, a := range addrs {
		p, _ := a.Prefix(rand.Intn(a.BitLen() + 1))
		routes[idx] = Route{MapTrieKey: MapTrieKey{Prefix: p.Masked()}}
	}
	ms0 := runtime.MemStats{}
	runtime.ReadMemStats(&ms0)
	mt := NewMapTrie(1024)
	ms := runtime.MemStats{}
	runtime.ReadMemStats(&ms)
	b.Logf("The initial Memory usage of MapTrie: %s", datasize.ByteSize(ms.HeapInuse-ms0.HeapInuse))
	b.ResetTimer()
	for range b.N {
		for idx := range routes {
			mt.InsertOrUpdate(routes[idx])
		}
	}
	b.StopTimer()
	ms2 := runtime.MemStats{}
	runtime.ReadMemStats(&ms2)
	uniq := 0
	for _, m := range mt {
		uniq += len(m)
	}
	var found int
	idx := max(rand.Intn(len(addrs))-1000, 0)
	for idx := range addrs[idx : idx+1000] {
		v, ok := mt.Lookup(addrs[idx])
		if !ok {
			panic("not found")
		}
		found += len(v.Routes)
	}
	b.Logf("Total number of routes %d: uniq %d", len(routes), uniq)
	b.Logf("Memory usage by mapTrie %s found=%d of 1k", datasize.ByteSize(ms2.HeapInuse-ms.HeapInuse), found)
}

var benchDataInsertmess = initTestData(1_000_000, 400_000, true)

func Benchmark_mapTrie_insert_mess(b *testing.B) {
	addrs := benchDataInsertmess
	routes := make([]Route, len(addrs))
	for idx, a := range addrs {
		p, _ := a.Prefix(rand.Intn(a.BitLen() + 1))
		routes[idx] = Route{MapTrieKey: MapTrieKey{Prefix: p.Masked()}}
	}
	ms0 := runtime.MemStats{}
	runtime.ReadMemStats(&ms0)
	mt := NewMapTrie(1024)
	ms := runtime.MemStats{}
	runtime.ReadMemStats(&ms)
	b.Logf("Initial Memory usage by mapTrie %s", datasize.ByteSize(ms.HeapInuse-ms0.HeapInuse))
	b.ResetTimer()
	for range b.N {
		for idx := range routes {
			mt.InsertOrUpdate(routes[idx])
		}
	}
	b.StopTimer()
	ms2 := runtime.MemStats{}
	runtime.ReadMemStats(&ms2)
	uniq := 0
	for _, m := range mt {
		uniq += len(m)
	}
	b.Logf("Total number of prefixes %d: uniq %d", len(routes), uniq)
	b.Logf("Memory usage by mapTrie %s", datasize.ByteSize(ms2.HeapInuse-ms.HeapInuse))
}

func Benchmark_mapTrie_lookup_mess_1k(b *testing.B) {
	addrs := initTestData(1_000_000, 400_000, true)
	routes := make([]Route, len(addrs))
	for idx, a := range addrs {
		p, _ := a.Prefix(rand.Intn(a.BitLen() + 1))
		routes[idx] = Route{MapTrieKey: MapTrieKey{Prefix: p.Masked()}}
	}

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

	uniq := 0
	for _, m := range mt {
		uniq += len(m)
	}

	b.Logf("Total number of prefixes %d: uniq: %d, found: %d", len(routes), uniq, found)
}
