//go:build linux && (amd64 || arm64)

package shmalloc_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
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
	// filler is the byte a region starts filled with, so the allocator
	// is not run on memory that happens to read zero.
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
		nativetest.Unpoison(mapping)
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

// newAllocatorRegion returns a region whose allocator holds the whole
// arena.
func newAllocatorRegion(t testing.TB) (*region, *layout.BlockAllocator) {
	t.Helper()
	memory := newRegion(t)
	allocator := (*layout.BlockAllocator)(memory.allocator())
	shmalloc.Init(allocator)
	shmalloc.PutArena(allocator, memory.arena(), arenaSize)
	return memory, allocator
}

// allocator returns the region's allocator at its start.
func (m *region) allocator() unsafe.Pointer {
	return m.base
}

// context returns the region's memory context idx, after the allocator.
func (m *region) context(idx int) unsafe.Pointer {
	return unsafe.Add(m.base, contextOffset+idx*int(unsafe.Sizeof(layout.MemoryContext{})))
}

// arena returns where the region's arena starts.
func (m *region) arena() unsafe.Pointer {
	return unsafe.Add(m.base, arenaOffset)
}

// Test_Constants_MatchC verifies that the native allocator uses the pool
// count, smallest block, arena alignment, red zone and largest block the
// C headers define for the build the views were generated from.
func Test_Constants_MatchC(t *testing.T) {
	require.Equal(t, int(layout.MemoryBlockAllocatorExp), shmalloc.PoolCount)
	require.Equal(t, int(layout.MemoryBlockAllocatorMinBits), shmalloc.MinBits)
	require.Equal(t, uintptr(layout.MemoryBlockAllocatorMaxAlign), uintptr(shmalloc.MaxAlign))
	require.Equal(t, uintptr(layout.AsanRedZone), uintptr(shmalloc.RedZone))
	require.Equal(t, uintptr(layout.MemoryBlockAllocatorMaxSize), uintptr(shmalloc.MaxSize))
}

// bytesOf views the bytes of a structure in place.
func bytesOf[T any](value *T) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(value)), unsafe.Sizeof(*value))
}

// Test_FreeSize_CountsArena verifies that the free bytes of a fresh
// allocator are the bytes of the arena it was given, down to one smallest
// block.
func Test_FreeSize_CountsArena(t *testing.T) {
	cases := []struct {
		name string
		size uintptr
	}{
		{name: "whole arena", size: arenaSize},
		{name: "one smallest block", size: 1 << shmalloc.MinBits},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newRegion(t)
			allocator := (*layout.BlockAllocator)(memory.allocator())
			shmalloc.Init(allocator)
			shmalloc.PutArena(allocator, memory.arena(), tc.size)
			require.Equal(t, tc.size, shmalloc.FreeSize(allocator))
		})
	}
}

// Test_Alloc_RefusesLeavingAllocatorAlone verifies that a request of no
// bytes, of more than the largest block or of more than any free block
// holds is refused, and that the allocator ends as it was.
func Test_Alloc_RefusesLeavingAllocatorAlone(t *testing.T) {
	cases := []struct {
		name string
		size uintptr
	}{
		{"zero size", 0},
		{"one byte over the largest block", shmalloc.MaxSize + 1},
		{"largest block with no free block that large", shmalloc.MaxSize},
		{"larger than the whole arena", arenaSize + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, allocator := newAllocatorRegion(t)
			image := bytesOf(allocator)
			before := bytes.Clone(image)

			require.Nil(t, shmalloc.Alloc(allocator, tc.size))

			require.Equal(t, before, image)
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

// churn allocates blocks of sizes and frees them again, rounds times,
// failing when a block's mark was overwritten while it was held.
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

// Test_Alloc_ConcurrentChurn verifies that goroutines allocating from one
// allocator at once never hand a block out twice and return every block
// they took.
func Test_Alloc_ConcurrentChurn(t *testing.T) {
	const (
		workers = 8
		rounds  = 200
	)
	_, allocator := newAllocatorRegion(t)
	free := shmalloc.FreeSize(allocator)
	sizes := churnSizes()

	var group errgroup.Group
	for worker := range workers {
		group.Go(func() error {
			return churn(allocator, sizes, rounds, byte(1+worker))
		})
	}

	require.NoError(t, group.Wait())
	require.Equal(t, free, shmalloc.FreeSize(allocator))
}
