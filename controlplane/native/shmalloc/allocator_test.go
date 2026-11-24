//go:build linux && (amd64 || arm64)

package shmalloc_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

const (
	// regionAlign aligns every region alike, since an arena is carved by
	// the alignment of its addresses.
	regionAlign   = 64 << 20
	contextOffset = 4 << 10
	contextCount  = 256
	arenaOffset   = 1 << 20
	arenaSize     = 24 << 20
	regionSize    = arenaOffset + arenaSize
	// filler is the byte a region starts filled with, so a byte one
	// implementation leaves unwritten differs from the one the other
	// writes instead of both reading zero.
	filler = 0xa5
)

// region is an anonymous mapping with an allocator at its start,
// contextCount memory contexts from contextOffset and an arena from
// arenaOffset.
type region struct {
	base unsafe.Pointer
}

// newRegion maps a region aligned to regionAlign, filled with filler, and
// unmaps it when the test ends.
func newRegion(t testing.TB) *region {
	t.Helper()
	require.LessOrEqual(t, unsafe.Sizeof(layout.BlockAllocator{}), uintptr(contextOffset),
		"the allocator overlaps the contexts",
	)
	require.LessOrEqual(t, contextOffset+contextCount*unsafe.Sizeof(layout.MemoryContext{}), uintptr(arenaOffset),
		"the contexts overlap the arena",
	)
	mapping, err := syscall.Mmap(-1, 0, regionSize+regionAlign,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		coracle.Unpoison(mapping)
		require.NoError(t, syscall.Munmap(mapping))
	})
	start := unsafe.Pointer(&mapping[0])
	pad := (regionAlign - uintptr(start)%regionAlign) % regionAlign
	memory := unsafe.Slice((*byte)(unsafe.Add(start, pad)), regionSize)
	memory[0] = filler
	for filled := 1; filled < len(memory); filled *= 2 {
		copy(memory[filled:], memory[:filled])
	}
	return &region{base: unsafe.Pointer(&memory[0])}
}

// allocator returns the region's allocator at its start.
func (m *region) allocator() unsafe.Pointer {
	return m.base
}

// context returns the region's memory context idx, after the allocator.
func (m *region) context(idx int) unsafe.Pointer {
	if idx >= contextCount {
		panic(fmt.Sprintf("context %d is past the region's %d", idx, contextCount))
	}
	return unsafe.Add(m.base, contextOffset+idx*int(unsafe.Sizeof(layout.MemoryContext{})))
}

// arena returns where the region's arena starts.
func (m *region) arena() unsafe.Pointer {
	return unsafe.Add(m.base, arenaOffset)
}

// state is a region's bytes and, in AddressSanitizer builds, the runs of
// it marked unaddressable.
type state struct {
	Image []byte
	Runs  []coracle.Run
}

// state returns a copy of the region's bytes and unaddressable runs.
func (m *region) state() state {
	image, runs := coracle.Snapshot(unsafe.Slice((*byte)(m.base), regionSize))
	return state{Image: image, Runs: runs}
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

// block is a block an operation run holds: its address and the size it
// was asked for.
type block struct {
	address unsafe.Pointer
	size    uintptr
}

// span is where an arena lies: its offset from the region's arena start
// and its size.
type span struct {
	offset uintptr
	size   uintptr
}

// wholeArena spans the region's whole arena, which starts 1 MiB aligned.
var wholeArena = span{0, arenaSize}

// run applies the operations to a fresh region with the whole arena, as
// runOn does.
func run(t testing.TB, operations []operation, pick func(idx int) implementation) ([]int64, state) {
	t.Helper()
	return runOn(t, wholeArena, operations, pick)
}

// runOn applies the operations to a fresh region whose allocator holds the
// arena at arena, pick choosing the implementation of each one (-1 for the
// setup), and returns where every allocation landed and the region's
// final state.
func runOn(t testing.TB, arena span, operations []operation, pick func(idx int) implementation) ([]int64, state) {
	t.Helper()
	memory := newRegion(t)
	setup := pick(-1)
	setup.init(memory.allocator())
	setup.putArena(memory.allocator(), unsafe.Add(memory.arena(), arena.offset), arena.size)

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
	return offsets, memory.state()
}

// onlyC picks C for every operation.
func onlyC(int) implementation {
	return cAllocator
}

// onlyGo picks Go for every operation.
func onlyGo(int) implementation {
	return goAllocator
}

// requireSameState fails on the first byte at which two regions differ,
// or when they differ in what is unaddressable.
func requireSameState(t testing.TB, want, got state) {
	t.Helper()
	require.Equal(t, want.Runs, got.Runs, "unaddressable runs differ")
	if bytes.Equal(want.Image, got.Image) {
		return
	}
	for idx := range want.Image {
		if want.Image[idx] != got.Image[idx] {
			t.Fatalf("images differ first at offset %#x: %#x, want %#x", idx, got.Image[idx], want.Image[idx])
		}
	}
}

// Test_Constants_MatchC verifies that the native allocator uses the pool
// count, smallest block, arena alignment, red zone and largest block of
// the C build it shares memory with.
func Test_Constants_MatchC(t *testing.T) {
	require.Equal(t, coracle.PoolCount, shmalloc.PoolCount)
	require.Equal(t, coracle.MinBits, shmalloc.MinBits)
	require.Equal(t, coracle.MaxAlign, shmalloc.MaxAlign)
	require.Equal(t, coracle.RedZone, shmalloc.RedZone)
	require.Equal(t, coracle.MaxSize, shmalloc.MaxSize)
}

// Test_Alloc_MatchesC verifies that the native allocator, given the same
// operations as the C one on an arena of the same alignment, returns the
// same blocks and leaves the same bytes, free lists and counters
// included.
func Test_Alloc_MatchesC(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3} {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			operations := randomOperations(seed, 20000)
			wantOffsets, wantState := run(t, operations, onlyC)
			gotOffsets, gotState := run(t, operations, onlyGo)
			require.Equal(t, wantOffsets, gotOffsets)
			requireSameState(t, wantState, gotState)
		})
	}
}

// Test_PutArena_MatchesC verifies that an arena of any start and size is
// rounded and carved into the blocks C carves it into, an arena with no
// aligned 8 bytes into none.
func Test_PutArena_MatchesC(t *testing.T) {
	cases := []struct {
		name  string
		arena span
	}{
		{"odd start", span{1, arenaSize - 1}},
		{"start 8 bytes past the alignment", span{8, arenaSize - 8}},
		{"start 64 bytes past the alignment", span{64, arenaSize - 64}},
		{"size not a multiple of 8", span{0, arenaSize - 3}},
		{"odd start and odd end", span{3, 1<<20 + 5}},
		{"under 8 bytes at an unaligned start", span{3, 4}},
		{"under 8 bytes at an aligned start", span{0, 7}},
		{"zero size at an unaligned start", span{5, 0}},
		{"zero size at an aligned start", span{0, 0}},
	}
	operations := randomOperations(6, 200)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantOffsets, wantState := runOn(t, tc.arena, operations, onlyC)
			gotOffsets, gotState := runOn(t, tc.arena, operations, onlyGo)
			require.Equal(t, wantOffsets, gotOffsets)
			requireSameState(t, wantState, gotState)
		})
	}
}

// Test_PutArena_PoisonsFreeBlocks verifies that in AddressSanitizer
// builds a fresh arena is left unaddressable, so the comparisons of what
// is unaddressable compare something.
func Test_PutArena_PoisonsFreeBlocks(t *testing.T) {
	if shmalloc.RedZone == 0 {
		t.Skip("only AddressSanitizer builds poison free blocks")
	}
	for _, candidate := range []implementation{cAllocator, goAllocator} {
		t.Run(candidate.name, func(t *testing.T) {
			_, got := run(t, nil, func(int) implementation { return candidate })
			require.NotEmpty(t, got.Runs)
		})
	}
}

// largestFreeBlock returns the size of the largest block the free lists
// of a fresh allocator over the whole arena hold.
func largestFreeBlock(t *testing.T) uintptr {
	t.Helper()
	memory := newRegion(t)
	allocator := (*layout.BlockAllocator)(memory.allocator())
	shmalloc.Init(allocator)
	shmalloc.PutArena(allocator, memory.arena(), arenaSize)
	for pool := len(allocator.Pools) - 1; pool >= 0; pool-- {
		if allocator.Pools[pool].Free != 0 {
			return 1 << (shmalloc.MinBits + pool)
		}
	}
	return 0
}

// Test_Alloc_RefusesAsC verifies that a request C refuses, Go refuses
// too, leaving the allocator as C leaves it.
func Test_Alloc_RefusesAsC(t *testing.T) {
	cases := []struct {
		name string
		size uintptr
		// exhausts marks a size no free block of the arena holds.
		exhausts bool
	}{
		{"zero size", 0, false},
		{"one byte over the largest block", shmalloc.MaxSize + 1, false},
		{"largest block with no free block that large", shmalloc.MaxSize, true},
		{"larger than the whole arena", arenaSize + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.exhausts {
				require.Greater(t, tc.size+2*shmalloc.RedZone, largestFreeBlock(t))
			}
			operations := []operation{{alloc: true, size: tc.size}}
			wantOffsets, wantState := run(t, operations, onlyC)
			gotOffsets, gotState := run(t, operations, onlyGo)
			require.Equal(t, []int64{-1}, wantOffsets)
			require.Equal(t, wantOffsets, gotOffsets)
			requireSameState(t, wantState, gotState)
		})
	}
}

// Test_Alloc_SharesFreeListsWithC verifies that C and Go alternating on
// one allocator, each freeing blocks the other allocated, end where C
// alone ends, whichever of them set the allocator up.
func Test_Alloc_SharesFreeListsWithC(t *testing.T) {
	cases := []struct {
		name  string
		setup implementation
	}{
		{"C sets up", cAllocator},
		{"Go sets up", goAllocator},
	}
	operations := randomOperations(4, 20000)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantOffsets, wantState := run(t, operations, onlyC)
			gotOffsets, gotState := run(t, operations, func(idx int) implementation {
				switch {
				case idx < 0:
					return tc.setup
				case idx%2 == 0:
					return goAllocator
				default:
					return cAllocator
				}
			})
			require.Equal(t, wantOffsets, gotOffsets)
			requireSameState(t, wantState, gotState)
		})
	}
}

// churnSizes returns the block sizes every worker of the concurrent test
// allocates each round: small enough that all workers together never
// exhaust the arena, though freed blocks are never merged.
func churnSizes() []uintptr {
	random := rand.New(rand.NewPCG(5, 5))
	sizes := make([]uintptr, 32)
	for idx := range sizes {
		sizes[idx] = uintptr(1 + random.IntN(4<<10))
	}
	return sizes
}

// churn is the Go side of the concurrent test: it allocates blocks of
// sizes and frees them again, rounds times, failing when a block's mark
// was overwritten while it was held.
func churn(allocator *layout.BlockAllocator, sizes []uintptr, rounds int, mark byte) error {
	blocks := make([][]byte, len(sizes))
	for range rounds {
		for idx, size := range sizes {
			address := shmalloc.Alloc(allocator, size)
			if address == nil {
				return fmt.Errorf("no block of %d bytes", size)
			}
			blocks[idx] = unsafe.Slice((*byte)(address), size)
			for pos := range blocks[idx] {
				blocks[idx][pos] = mark
			}
		}
		for idx, block := range blocks {
			for pos, value := range block {
				if value != mark {
					return fmt.Errorf("block of %d bytes overwritten at %d", len(block), pos)
				}
			}
			shmalloc.Free(allocator, unsafe.Pointer(&block[0]), sizes[idx])
		}
	}
	return nil
}

// Test_Alloc_ConcurrentWithC verifies that goroutines and C threads
// allocating from one allocator at once never hand a block out twice and
// return every block they took.
func Test_Alloc_ConcurrentWithC(t *testing.T) {
	const (
		workers = 4
		rounds  = 200
	)
	memory := newRegion(t)
	allocator := (*layout.BlockAllocator)(memory.allocator())
	shmalloc.Init(allocator)
	shmalloc.PutArena(allocator, memory.arena(), arenaSize)
	free := shmalloc.FreeSize(allocator)
	sizes := churnSizes()

	var group errgroup.Group
	for worker := range workers {
		group.Go(func() error {
			return churn(allocator, sizes, rounds, byte(1+worker))
		})
		group.Go(func() error {
			overwritten, err := coracle.Churn(memory.allocator(), sizes, rounds, byte(128+worker))
			if err != nil {
				return err
			}
			if overwritten != 0 {
				return fmt.Errorf("C found %d blocks overwritten", overwritten)
			}
			return nil
		})
	}

	require.NoError(t, group.Wait())
	require.Equal(t, free, shmalloc.FreeSize(allocator))
	require.Equal(t, free, coracle.FreeSize(memory.allocator()))
}

// FuzzAlloc_MatchesC verifies that any sequence of allocations and frees
// leaves the native allocator where it leaves the C one.
func FuzzAlloc_MatchesC(f *testing.F) {
	// A build that sanitizes the C oracle but not Go gives the two sides
	// different red zones, which no comparison can reconcile; the
	// constants test is what reports such a build.
	if coracle.RedZone != shmalloc.RedZone {
		f.Skip("the C oracle and Go were built with different red zones")
	}
	f.Add([]byte{1, 64, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{49, 0, 0, 0, 1, 49, 0, 0, 0, 1, 0, 1, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		operations := decodeOperations(data)
		wantOffsets, wantState := run(t, operations, onlyC)
		gotOffsets, gotState := run(t, operations, onlyGo)
		require.Equal(t, wantOffsets, gotOffsets)
		requireSameState(t, wantState, gotState)
	})
}
