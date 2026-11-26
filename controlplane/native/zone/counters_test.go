//go:build linux && (amd64 || arm64)

package zone_test

import (
	"fmt"
	"net"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
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
// are the later storage's, and storages or values that cannot be reached
// are skipped or read as zero.
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

// counterGroup returns a group of two-worker counters under the tags,
// given as key and value pairs, whose counters named as hit count one
// frame of the size given on the second worker.
//
// Histograms hold eight values, every other counter two: packets and
// bytes.
func counterGroup(tags []string, hit []string, frameSize uint64, counters ...string) zone.CounterGroup {
	group := zone.CounterGroup{}
	for pair := range slices.Chunk(tags, 2) {
		group.Tags = append(group.Tags, zone.CounterTag{Key: pair[0], Value: pair[1]})
	}
	for _, name := range counters {
		size := 2
		if strings.HasSuffix(name, "histogram") {
			size = 8
		}
		values := [][]uint64{make([]uint64, size), make([]uint64, size)}
		if slices.Contains(hit, name) {
			values[1] = []uint64{1, frameSize}
		}
		group.Counters = append(group.Counters, zone.Counter{Name: name, Values: values})
	}
	return group
}

// publishedCounters returns, by a name of each, the counter groups of the
// published topology after one frame of the size given ran on the second
// worker and the first module dropped it.
func publishedCounters(frameSize uint64) map[string]zone.CounterGroup {
	var device []string
	for _, direction := range []string{"input", "output"} {
		for _, name := range []string{"rx", "entry", "tx", "drop", "recirc_drop", "pending_input", "pending_output"} {
			device = append(device, direction+"_"+name)
		}
	}
	flow := []string{"input", "output", "drop", "input histogram", "pending_input", "pending_output"}
	return map[string]zone.CounterGroup{
		"device": counterGroup(
			[]string{"device", "port0", "kind", "device"},
			[]string{"input_rx", "input_entry", "input_drop"}, frameSize, device...,
		),
		"input pipeline": counterGroup(
			[]string{"device", "port0", "kind", "pipeline", "pipeline", "drop"},
			[]string{"input", "drop"}, frameSize, flow...,
		),
		"output pipeline": counterGroup(
			[]string{"device", "port0", "kind", "pipeline", "pipeline", "dummy"},
			nil, frameSize, flow...,
		),
		"function": counterGroup(
			[]string{"device", "port0", "function", "drop", "kind", "function", "pipeline", "drop"},
			[]string{"input", "drop"}, frameSize, flow...,
		),
		"chain": counterGroup(
			[]string{"chain", "drop_chain", "device", "port0", "function", "drop", "kind", "chain", "pipeline", "drop"},
			nil, frameSize, "pending_input", "pending_output",
		),
		"module": counterGroup(
			[]string{
				"chain", "drop_chain", "device", "port0", "function", "drop", "kind", "module",
				"module_name", "first", "module_type", "blackhole", "pipeline", "drop",
			},
			[]string{"rx", "drop"}, frameSize, "rx", "tx", "drop", "pending_input", "pending_output",
		),
	}
}

// Test_Generation_CountersSelectsByTags verifies that the counter reader
// reports, for every tag predicate shape, the groups, counters and
// per-worker values of the published topology, after one frame ran on
// one worker only, with an object whose counter storages hold no
// counters.
func Test_Generation_CountersSelectsByTags(t *testing.T) {
	memory := newPublished(t)
	object := (*layout.CpObject)(shmalloc.ContextAlloc(&memory.Owner.Agent.MemoryContext, unsafe.Sizeof(layout.CpObject{})))
	require.NotNil(t, object)
	require.NoError(t, cpconfig.InitObject(object, memory.Owner, "ring", "state"))
	require.NoError(t, cpconfig.UpdateObjects(memory.Instance, []*layout.CpObject{object}, memory.Deliver()))
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := layers.IPv4{
		Version: 4, TTL: 64, Protocol: layers.IPProtocolICMPv4,
		SrcIP: net.IPv4(10, 0, 0, 1), DstIP: net.IPv4(192, 168, 1, 1),
	}
	icmp := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	frame := xpacket.LayersToPacket(t, &eth, &ip, &icmp).Data()
	result, err := memory.Dataplane.Round(1, 0, frame)
	require.NoError(t, err)
	require.Len(t, result.Drop, 1)
	pinned, err := memory.Instance.Pin()
	require.NoError(t, err)
	t.Cleanup(pinned.Release)
	groups := publishedCounters(uint64(len(frame)))
	every := []string{"device", "input pipeline", "output pipeline", "function", "chain", "module"}

	cases := []struct {
		name       string
		predicates []zone.CounterTag
		// groups names the groups the predicates select.
		groups []string
	}{
		{"every storage", nil, every},
		{
			"module storages of a type",
			[]zone.CounterTag{{Key: "kind", Value: "module"}, {Key: "module_type", Value: "blackhole"}},
			[]string{"module"},
		},
		{
			"one module by its position",
			[]zone.CounterTag{
				{Key: "module_name", Value: "first"}, {Key: "chain", Value: "drop_chain"}, {Key: "kind", Value: "module"},
			},
			[]string{"module"},
		},
		{"storages with a key at any value", []zone.CounterTag{{Key: "chain", Value: "*"}}, []string{"chain", "module"}},
		{"storages without a key", []zone.CounterTag{{Key: "pipeline", Value: ""}}, []string{"device"}},
		{"a value no storage has", []zone.CounterTag{{Key: "module_name", Value: "missing"}}, nil},
		{"an empty key absent", []zone.CounterTag{{Key: "", Value: ""}}, every},
		{"an empty key with a value", []zone.CounterTag{{Key: "", Value: "module"}}, nil},
		{"object storages without counters", []zone.CounterTag{{Key: "kind", Value: "object"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var want []zone.CounterGroup
			for _, name := range tc.groups {
				want = append(want, groups[name])
			}
			got, err := pinned.Counters(tc.predicates)
			require.NoError(t, err)
			require.ElementsMatch(t, want, got)
		})
	}
}

// Test_Instance_CurrentTimeReadsWorkersTime verifies that the reader
// reports the time the dataplane's workers published.
func Test_Instance_CurrentTimeReadsWorkersTime(t *testing.T) {
	memory := newPublished(t)
	now := time.Unix(1_700_000_000, 5)
	require.NoError(t, memory.Dataplane.SetTime(now))

	got, ok := memory.Instance.CurrentTime()

	require.True(t, ok)
	require.Equal(t, uint64(now.UnixNano()), got)
}
