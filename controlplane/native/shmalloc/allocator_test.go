//go:build linux && amd64 && !asan

package shmalloc_test

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// regionAlign aligns every region alike, since an arena is carved by
	// the alignment of its addresses.
	regionAlign   = 64 << 20
	contextOffset = 4 << 10
	arenaOffset   = 1 << 20
	arenaSize     = 24 << 20
	regionSize    = arenaOffset + arenaSize
)

// region is an anonymous mapping with an allocator at its start, memory
// contexts after it and an arena from arenaOffset.
type region struct {
	base unsafe.Pointer
}

// newRegion maps a zeroed region aligned to regionAlign.
func newRegion(t testing.TB) *region {
	t.Helper()
	mapping, err := syscall.Mmap(-1, 0, regionSize+regionAlign,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, syscall.Munmap(mapping)) })
	start := unsafe.Pointer(&mapping[0])
	pad := (regionAlign - uintptr(start)%regionAlign) % regionAlign
	return &region{base: unsafe.Add(start, pad)}
}

func (m *region) allocator() unsafe.Pointer {
	return m.base
}

func (m *region) context(idx int) unsafe.Pointer {
	return unsafe.Add(m.base, contextOffset+idx*int(unsafe.Sizeof(layout.MemoryContext{})))
}

func (m *region) arena() unsafe.Pointer {
	return unsafe.Add(m.base, arenaOffset)
}

// image returns a copy of the region's bytes.
func (m *region) image() []byte {
	return bytes.Clone(unsafe.Slice((*byte)(m.base), regionSize))
}

// offset returns where a block lies in the region, or -1 for nil.
func (m *region) offset(block unsafe.Pointer) int64 {
	if block == nil {
		return -1
	}
	return int64(uintptr(block) - uintptr(m.base))
}

// implementation is one way to run allocator operations.
type implementation struct {
	name     string
	init     func(allocator unsafe.Pointer)
	putArena func(allocator, arena unsafe.Pointer, size uintptr)
	alloc    func(allocator unsafe.Pointer, size uintptr) unsafe.Pointer
	free     func(allocator, block unsafe.Pointer, size uintptr)
}

var cAllocator = implementation{
	name:     "C",
	init:     coracle.BlockAllocatorInit,
	putArena: coracle.PutArena,
	alloc:    coracle.Alloc,
	free:     coracle.Free,
}

var goAllocator = implementation{
	name: "Go",
	init: func(allocator unsafe.Pointer) {
		shmalloc.Init((*layout.BlockAllocator)(allocator))
	},
	putArena: func(allocator, arena unsafe.Pointer, size uintptr) {
		shmalloc.PutArena((*layout.BlockAllocator)(allocator), arena, size)
	},
	alloc: func(allocator unsafe.Pointer, size uintptr) unsafe.Pointer {
		return shmalloc.Alloc((*layout.BlockAllocator)(allocator), size)
	},
	free: func(allocator, block unsafe.Pointer, size uintptr) {
		shmalloc.Free((*layout.BlockAllocator)(allocator), block, size)
	},
}

// operation allocates size bytes, or frees the live block victim picks.
type operation struct {
	alloc  bool
	size   uintptr
	victim uint32
}

// randomOperations returns a reproducible mix of allocations of sizes
// across every pool and frees of live blocks.
func randomOperations(seed uint64, count int) []operation {
	random := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	operations := make([]operation, count)
	for idx := range operations {
		if random.IntN(10) < 4 {
			operations[idx] = operation{victim: random.Uint32()}
			continue
		}
		var size uintptr
		switch class := random.IntN(20); {
		case class < 10:
			size = uintptr(1 + random.IntN(256))
		case class < 16:
			size = uintptr(257 + random.IntN(16<<10))
		case class < 19:
			size = uintptr(16<<10 + random.IntN(1<<20))
		default:
			size = uintptr(1<<20 + random.IntN(8<<20))
		}
		operations[idx] = operation{alloc: true, size: size}
	}
	return operations
}

// decodeOperations turns fuzz input into operations, five bytes each.
func decodeOperations(data []byte) []operation {
	var operations []operation
	for len(data) >= 5 {
		value := binary.LittleEndian.Uint32(data[1:5])
		if data[0]&1 == 0 {
			operations = append(operations, operation{victim: value})
		} else {
			// Sizes up to 16 MiB, with small ones as likely as large.
			shift := uint(data[0]>>1) % 25
			operations = append(operations, operation{alloc: true, size: uintptr(1 + value%(1<<shift))})
		}
		data = data[5:]
	}
	return operations
}

type block struct {
	address unsafe.Pointer
	size    uintptr
}

// run applies the operations to a fresh region, pick choosing the
// implementation of each one (-1 for the setup), and returns where every
// allocation landed and the region's final bytes.
func run(t testing.TB, operations []operation, pick func(idx int) implementation) ([]int64, []byte) {
	t.Helper()
	memory := newRegion(t)
	setup := pick(-1)
	setup.init(memory.allocator())
	setup.putArena(memory.allocator(), memory.arena(), arenaSize)

	var offsets []int64
	var live []block
	for idx, op := range operations {
		implementation := pick(idx)
		if op.alloc {
			address := implementation.alloc(memory.allocator(), op.size)
			offsets = append(offsets, memory.offset(address))
			if address != nil {
				live = append(live, block{address, op.size})
			}
			continue
		}
		if len(live) == 0 {
			continue
		}
		victim := int(op.victim % uint32(len(live)))
		implementation.free(memory.allocator(), live[victim].address, live[victim].size)
		live[victim] = live[len(live)-1]
		live = live[:len(live)-1]
	}
	return offsets, memory.image()
}

// requireSameImage fails on the first byte at which two images differ.
func requireSameImage(t testing.TB, want, got []byte) {
	t.Helper()
	if bytes.Equal(want, got) {
		return
	}
	for idx := range want {
		if want[idx] != got[idx] {
			t.Fatalf("images differ first at offset %#x: %#x, want %#x", idx, got[idx], want[idx])
		}
	}
}

// Test_Constants_MatchC verifies that the native allocator uses the pool
// count, red zone and largest block of the C build it shares memory with.
func Test_Constants_MatchC(t *testing.T) {
	require.Equal(t, coracle.PoolCount, shmalloc.PoolCount)
	require.Equal(t, coracle.RedZone, shmalloc.RedZone)
	require.Equal(t, coracle.MaxSize, shmalloc.MaxSize)
}

// Test_Alloc_MatchesC verifies that the native allocator, given the same
// operations as the C one on an arena of the same alignment, returns the
// same blocks and leaves the same bytes, free lists and counters
// included.
func Test_Alloc_MatchesC(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3} {
		operations := randomOperations(seed, 20000)
		wantOffsets, wantImage := run(t, operations, func(int) implementation { return cAllocator })
		gotOffsets, gotImage := run(t, operations, func(int) implementation { return goAllocator })
		require.Equal(t, wantOffsets, gotOffsets, "seed %d", seed)
		requireSameImage(t, wantImage, gotImage)
	}
}

// Test_Alloc_SharesFreeListsWithC verifies that C and Go alternating on
// one allocator, each freeing blocks the other allocated, end where C
// alone ends.
func Test_Alloc_SharesFreeListsWithC(t *testing.T) {
	operations := randomOperations(4, 20000)
	wantOffsets, wantImage := run(t, operations, func(int) implementation { return cAllocator })
	gotOffsets, gotImage := run(t, operations, func(idx int) implementation {
		if idx%2 == 0 {
			return goAllocator
		}
		return cAllocator
	})
	require.Equal(t, wantOffsets, gotOffsets)
	requireSameImage(t, wantImage, gotImage)
}

// FuzzAlloc_MatchesC verifies that any sequence of allocations and frees
// leaves the native allocator where it leaves the C one.
func FuzzAlloc_MatchesC(f *testing.F) {
	f.Add([]byte{1, 64, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{49, 0, 0, 0, 1, 49, 0, 0, 0, 1, 0, 1, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		operations := decodeOperations(data)
		wantOffsets, wantImage := run(t, operations, func(int) implementation { return cAllocator })
		gotOffsets, gotImage := run(t, operations, func(int) implementation { return goAllocator })
		require.Equal(t, wantOffsets, gotOffsets)
		requireSameImage(t, wantImage, gotImage)
	})
}

// contextImplementation is one way to run memory-context operations.
type contextImplementation struct {
	init     func(context unsafe.Pointer, name string, allocator unsafe.Pointer)
	initFrom func(context, parent unsafe.Pointer, name string)
	fini     func(context unsafe.Pointer)
	alloc    func(context unsafe.Pointer, size uintptr) unsafe.Pointer
	free     func(context, block unsafe.Pointer, size uintptr)
}

var cContext = contextImplementation{
	init:     coracle.ContextInit,
	initFrom: coracle.ContextInitFrom,
	fini:     coracle.ContextFini,
	alloc:    coracle.ContextAlloc,
	free:     coracle.ContextFree,
}

var goContext = contextImplementation{
	init: func(context unsafe.Pointer, name string, allocator unsafe.Pointer) {
		shmalloc.ContextInit((*layout.MemoryContext)(context), name, (*layout.BlockAllocator)(allocator))
	},
	initFrom: func(context, parent unsafe.Pointer, name string) {
		shmalloc.ContextInitFrom((*layout.MemoryContext)(context), (*layout.MemoryContext)(parent), name)
	},
	fini: func(context unsafe.Pointer) {
		shmalloc.ContextFini((*layout.MemoryContext)(context))
	},
	alloc: func(context unsafe.Pointer, size uintptr) unsafe.Pointer {
		return shmalloc.ContextAlloc((*layout.MemoryContext)(context), size)
	},
	free: func(context, block unsafe.Pointer, size uintptr) {
		shmalloc.ContextFree((*layout.MemoryContext)(context), block, size)
	},
}

// runContexts builds a root context with three children, allocates and
// frees through them, finalises the middle child and returns the region's
// bytes.
func runContexts(t *testing.T, implementation contextImplementation) []byte {
	t.Helper()
	memory := newRegion(t)
	coracle.BlockAllocatorInit(memory.allocator())
	coracle.PutArena(memory.allocator(), memory.arena(), arenaSize)

	implementation.init(memory.context(0), "root", memory.allocator())
	names := []string{"first", "second", "a name longer than the sixty-three bytes a context keeps for it"}
	for idx, name := range names {
		implementation.initFrom(memory.context(idx+1), memory.context(0), name)
	}
	var blocks []block
	for idx := range 300 {
		context := memory.context(1 + idx%3)
		size := uintptr(8 + idx*37%4096)
		blocks = append(blocks, block{implementation.alloc(context, size), size})
	}
	for idx := 0; idx < len(blocks); idx += 2 {
		implementation.free(memory.context(1+idx%3), blocks[idx].address, blocks[idx].size)
	}
	implementation.fini(memory.context(2))
	return memory.image()
}

// Test_Context_MatchesC verifies that memory contexts initialised,
// counted, linked into a tree and finalised by Go leave the bytes C
// leaves.
func Test_Context_MatchesC(t *testing.T) {
	requireSameImage(t, runContexts(t, cContext), runContexts(t, goContext))
}
