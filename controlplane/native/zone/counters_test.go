//go:build linux && (amd64 || arm64)

package zone_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_Generation_CountersRefusesPredicates verifies that predicates the
// fixed-size C tags cannot carry, or that name a key twice, are refused
// before anything is read.
func Test_Generation_CountersRefusesPredicates(t *testing.T) {
	tooMany := make([]zone.CounterTag, len(layout.CpCounterStorage{}.Tags)+1)
	for idx := range tooMany {
		tooMany[idx] = zone.CounterTag{Key: fmt.Sprintf("key%d", idx), Value: "*"}
	}
	keyLen := len(layout.CounterTag{}.Key)
	valueLen := len(layout.CounterTag{}.Value)
	cases := []struct {
		name       string
		predicates []zone.CounterTag
		message    string
	}{
		{"one key twice", []zone.CounterTag{{Key: "kind", Value: "module"}, {Key: "kind", Value: "device"}}, "more than once"},
		{"more predicates than a storage has tags", tooMany, "more than"},
		{"NUL byte in the key", []zone.CounterTag{{Key: "ki\x00nd", Value: "module"}}, "NUL byte"},
		{"NUL byte in the value", []zone.CounterTag{{Key: "kind", Value: "mod\x00ule"}}, "NUL byte"},
		{"key one byte too long", []zone.CounterTag{{Key: strings.Repeat("k", keyLen), Value: "*"}}, "key"},
		{"value one byte too long", []zone.CounterTag{{Key: "kind", Value: strings.Repeat("v", valueLen)}}, "value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var generation zone.Generation
			_, err := generation.Counters(tc.predicates)
			require.ErrorContains(t, err, tc.message)
		})
	}
}

// Test_Generation_CountersAcceptsLongestTags verifies that a key and a
// value one byte shorter than their C fields are accepted.
func Test_Generation_CountersAcceptsLongestTags(t *testing.T) {
	var generation zone.Generation
	_, err := generation.Counters([]zone.CounterTag{{
		Key:   strings.Repeat("k", len(layout.CounterTag{}.Key)-1),
		Value: strings.Repeat("v", len(layout.CounterTag{}.Value)-1),
	}})
	require.NoError(t, err)
}

// arena is Go memory laid out as a crafted shared-memory image: relative
// pointers between its blocks stay valid as long as it lives.
type arena struct {
	memory []uint64
	used   int
}

// alloc returns a zeroed, 8-byte aligned block for count values of T.
func alloc[T any](memory *arena, count int) []T {
	var zero T
	words := (int(unsafe.Sizeof(zero))*count + 7) / 8
	block := unsafe.Pointer(&memory.memory[memory.used])
	memory.used += words
	return unsafe.Slice((*T)(block), count)
}

// craftedCounterStorage returns a storage of one counter of one value,
// registered under the tags in the item.
func craftedCounterStorage(memory *arena, item *layout.CpCounterStorage, name string, value uint64) {
	storage := &alloc[layout.CounterStorage](memory, 1)[0]
	registry := &alloc[layout.CounterRegistry](memory, 1)[0]
	names := alloc[layout.Counter](memory, 1)
	handles := alloc[shm.Ptr[byte]](memory, 1)
	values := alloc[uint64](memory, 1)
	copy(names[0].Name[:], name)
	names[0].Size = 1
	values[0] = value
	handles[0].Set((*byte)(unsafe.Pointer(&values[0])))
	registry.Count = 1
	registry.Names.Set(&names[0])
	storage.Registry.Set(registry)
	storage.CounterValueHandles.Set(&handles[0])
	item.Storage.Set(storage)
}

// craftedGeneration returns a generation of one worker whose counter
// storage registry holds the items, each tagged module_name=first.
func craftedGeneration(memory *arena, itemCount int) (*layout.CpConfigGen, []layout.CpCounterStorage) {
	gen := &alloc[layout.CpConfigGen](memory, 1)[0]
	dp := &alloc[layout.DpConfig](memory, 1)[0]
	roots := alloc[shm.Ptr[layout.ConfigGenEctx]](memory, 1)
	root := &alloc[layout.ConfigGenEctx](memory, 1)[0]
	registry := &alloc[layout.CpConfigCounterStorageRegistry](memory, 1)[0]
	items := alloc[layout.CpCounterStorage](memory, itemCount)
	for idx := range items {
		copy(items[idx].Tags[0].Key[:], "module_name")
		copy(items[idx].Tags[0].Value[:], "first")
		items[idx].TagCount = 1
	}
	dp.WorkerCount = 1
	gen.DpConfig.Set(dp)
	gen.ConfigGenEctxCount = 1
	gen.ConfigGenEctxs.Set(&roots[0])
	roots[0].Set(root)
	root.CounterStorageRegistry.Set(registry)
	registry.Count = uint64(itemCount)
	registry.Items.Set(&items[0])
	return gen, items
}

// Test_Generation_CountersOfCraftedGeneration verifies how storages of
// one worker are read: two of one tag set give one group whose values
// are the later storage's, as the cgo reader's merge gives them, and
// storages or values that cannot be reached are skipped or read as zero.
func Test_Generation_CountersOfCraftedGeneration(t *testing.T) {
	tags := []zone.CounterTag{{Key: "module_name", Value: "first"}}
	cases := []struct {
		name  string
		build func(memory *arena) *layout.CpConfigGen
		want  []zone.CounterGroup
	}{
		{
			"two storages of one tag set",
			func(memory *arena) *layout.CpConfigGen {
				gen, items := craftedGeneration(memory, 2)
				craftedCounterStorage(memory, &items[0], "packets", 1)
				craftedCounterStorage(memory, &items[1], "packets", 2)
				return gen
			},
			[]zone.CounterGroup{{Tags: tags, Counters: []zone.Counter{{Name: "packets", Values: [][]uint64{{2}}}}}},
		},
		{
			"storage missing",
			func(memory *arena) *layout.CpConfigGen {
				gen, _ := craftedGeneration(memory, 1)
				return gen
			},
			nil,
		},
		{
			"value handles missing",
			func(memory *arena) *layout.CpConfigGen {
				gen, items := craftedGeneration(memory, 1)
				craftedCounterStorage(memory, &items[0], "packets", 1)
				items[0].Storage.Get().CounterValueHandles.Set(nil)
				return gen
			},
			[]zone.CounterGroup{{Tags: tags, Counters: []zone.Counter{{Name: "packets", Values: [][]uint64{{0}}}}}},
		},
		{
			"tag count beyond the tag array",
			func(memory *arena) *layout.CpConfigGen {
				gen, items := craftedGeneration(memory, 1)
				craftedCounterStorage(memory, &items[0], "packets", 1)
				items[0].TagCount = 1 << 40
				return gen
			},
			nil,
		},
		{
			"registry items missing",
			func(memory *arena) *layout.CpConfigGen {
				gen, _ := craftedGeneration(memory, 1)
				gen.ConfigGenEctxs.Get().Get().CounterStorageRegistry.Get().Items.Set(nil)
				return gen
			},
			nil,
		},
		{
			"dataplane zone missing",
			func(memory *arena) *layout.CpConfigGen {
				gen, items := craftedGeneration(memory, 1)
				craftedCounterStorage(memory, &items[0], "packets", 1)
				gen.DpConfig.Set(nil)
				return gen
			},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := &arena{memory: make([]uint64, 1<<16)}
			generation := zone.Generation{Gen: tc.build(memory)}
			got, err := generation.Counters(tags)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			runtime.KeepAlive(memory)
		})
	}
}
