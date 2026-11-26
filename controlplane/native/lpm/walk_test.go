//go:build linux && (amd64 || arm64)

package lpm_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// stretch is a run of keys a walk visited with one value, its keys
// copied out of the walk.
type stretch struct {
	From  []byte
	To    []byte
	Value uint32
}

// lookup returns the value a tree maps a key to, read as the dataplane's
// lookup reads it: down the pages, a key byte each, to the first slot
// holding a value.
func lookup(tree *layout.Lpm, key []byte) uint32 {
	current := tree.Pages.Get().Get()
	var slot uint64
	for _, keyByte := range key {
		slot = *(*uint64)(unsafe.Pointer(&current.Values[keyByte]))
		if slot&1 != 0 {
			break
		}
		current = (*shm.Ptr[layout.LpmPage])(unsafe.Pointer(&current.Values[keyByte])).Get()
	}
	return uint32(slot >> 1)
}

// nextKey returns the key after the given one, or false past the last
// key.
func nextKey(key []byte) ([]byte, bool) {
	next := bytes.Clone(key)
	for idx := len(next) - 1; idx >= 0; idx-- {
		next[idx]++
		if next[idx] != 0 {
			return next, true
		}
	}
	return next, false
}

// randomRanges returns one to six inserts of random values whose key
// bytes come mostly from page edges and their neighbours, so the ranges
// nest, overlap and meet.
func randomRanges(random *rand.Rand, keySize int) []keyRange {
	edges := []byte{0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff}
	randomKey := func() []byte {
		key := make([]byte, keySize)
		for idx := range key {
			key[idx] = edges[random.IntN(len(edges))]
			if random.IntN(4) == 0 {
				key[idx] = byte(random.Uint32())
			}
		}
		return key
	}
	ranges := make([]keyRange, 1+random.IntN(6))
	for idx := range ranges {
		from, to := randomKey(), randomKey()
		if bytes.Compare(from, to) > 0 {
			from, to = to, from
		}
		ranges[idx] = keyRange{From: from, To: to, Value: random.Uint32()}
	}
	return ranges
}

// requireWalkMatchesLookup asserts that the walk of a tree built from the
// ranges visits every key once, in ascending order, with the value the
// tree's lookup gives it.
//
// The stretches must tile the key space from the zero key through the
// last key. The lookup changes only at a range's start and past its end,
// so checking it there and at each stretch's start against the stretch
// holding the key covers every key.
func requireWalkMatchesLookup(t *testing.T, ranges []keyRange, keySize int) {
	t.Helper()
	memory := newTreeMemory()
	require.NoError(t, lpm.Init(&memory.Tree, &memory.Context, "tree"))
	for _, insert := range ranges {
		require.NoError(t, lpm.Insert(&memory.Tree, insert.From, insert.To, insert.Value))
	}

	var stretches []stretch
	lpm.Walk(&memory.Tree, keySize, func(from, to []byte, value uint32) {
		stretches = append(stretches, stretch{From: bytes.Clone(from), To: bytes.Clone(to), Value: value})
	})

	next, more := make([]byte, keySize), true
	for _, visited := range stretches {
		require.True(t, more, "a stretch past the last key")
		require.Equal(t, next, visited.From, "a stretch starts right after the one before")
		require.LessOrEqual(t, bytes.Compare(visited.From, visited.To), 0, "a stretch ends before its start")
		next, more = nextKey(visited.To)
	}
	require.False(t, more, "the last stretch ends at the last key")

	keys := make([][]byte, 0, len(stretches)+2*len(ranges))
	for _, visited := range stretches {
		keys = append(keys, visited.From)
	}
	for _, insert := range ranges {
		keys = append(keys, insert.From)
		if past, ok := nextKey(insert.To); ok {
			keys = append(keys, past)
		}
	}
	for _, key := range keys {
		idx, _ := slices.BinarySearchFunc(stretches, key, func(visited stretch, key []byte) int {
			return bytes.Compare(visited.To, key)
		})
		require.Equal(t, lookup(&memory.Tree, key), stretches[idx].Value, "the value of key %x", key)
	}
}

// Test_Walk_Trees verifies that walking an empty tree, nested IPv4 and
// IPv6 prefixes and a tree that grew its chunk directory visits every key
// once, in order, with its lookup value.
func Test_Walk_Trees(t *testing.T) {
	cases := []struct {
		name    string
		ranges  []keyRange
		keySize int
	}{
		{name: "no ranges", keySize: 4},
		{name: "nested ipv4 prefixes", keySize: 4, ranges: []keyRange{
			prefixRange("10.0.0.0/8", 1),
			prefixRange("10.1.2.128/25", 2),
			prefixRange("0.0.0.0/0", 3),
			prefixRange("10.1.0.0/16", 4),
		}},
		{name: "ipv6 prefixes", keySize: 16, ranges: []keyRange{
			prefixRange("2001:db8::/32", 1),
			prefixRange("2001:db8:1::/48", 2),
			prefixRange("::1/128", 3),
			prefixRange("fe80::/10", 4),
		}},
		{name: "chunk directory grown", keySize: 4, ranges: chunkedRanges(40)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireWalkMatchesLookup(t, tc.ranges, tc.keySize)
		})
	}
}

// Test_Walk_RandomTrees verifies that walking random trees of keys from 1
// to 16 bytes visits every key once, in order, with its lookup value.
func Test_Walk_RandomTrees(t *testing.T) {
	for _, keySize := range []int{1, 2, 3, 4, 8, 16} {
		for seed := range uint64(8) {
			t.Run(fmt.Sprintf("%d-byte keys, seed %d", keySize, seed), func(t *testing.T) {
				random := rand.New(rand.NewPCG(seed, uint64(keySize)))
				requireWalkMatchesLookup(t, randomRanges(random, keySize), keySize)
			})
		}
	}
}
