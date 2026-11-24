//go:build linux && (amd64 || arm64)

// Package shmalloc allocates YANET shared memory as the C block allocator
// and memory contexts of common/memory do.
//
// Its free lists are the C ones: a Go process and a C process allocate
// from one allocator and free each other's blocks. That makes C's
// algorithm, not only its layout, the contract, and the port keeps every
// choice of common/memory_block.h and common/memory.h so that the same
// operations leave byte-identical storages:
//
//   - Pools hold blocks of 8 << pool bytes; a request takes the smallest
//     pool that holds it, and there is no merging of free neighbours, so
//     the order of frees shapes what later allocations get.
//   - An empty pool is refilled from the nearest larger pool that has a
//     block, splitting one block per pool on the way down; the lower half
//     heads the free list, the upper half follows it.
//   - A free block's first word is a relative pointer to the next free
//     block of its pool, and a freed block goes to the head of its list.
//   - An arena is rounded inwards to 8 bytes and cut, from its start,
//     into the largest blocks the alignment of each position allows,
//     the largest pool's at 2 MiB alignment.
//   - Pools count allocations, free blocks and splits as C does.
//
// The allocator's 32-bit spinlock guards its free lists and also the tree
// of memory contexts sharing it, which is spliced under it as C does.
// Context counters are updated with atomic adds, as C updates them.
//
// Red zones follow the Go build, as they follow the C build: under -asan
// every block carries the AddressSanitizer build's guard on both sides,
// and free blocks and link words are poisoned and unpoisoned where C does
// it, through the sanitizer's interface, the one use of cgo here. Only
// the sides of one process agree by construction: nothing in shared
// memory records the red zone, so a Go process sharing an allocator with
// a separate C process must be built with the same sanitizer setting.
package shmalloc

import (
	"errors"
	"math/bits"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// ErrNoMemory marks shared memory a build or a publication ran out of.
//
// Every native package's own exhaustion error wraps it, so a caller tells
// shared memory running out from any other failure with one sentinel.
var ErrNoMemory = errors.New("shared memory exhausted")

// PoolCount is the number of block sizes (MEMORY_BLOCK_ALLOCATOR_EXP),
// taken from the allocator's layout.
const PoolCount = len(layout.BlockAllocator{}.Pools)

// linkSize is the size of the link word a free block starts with.
const linkSize = unsafe.Sizeof(shm.Ptr[byte]{})

// MinBits is the binary logarithm of the smallest block size
// (MEMORY_BLOCK_ALLOCATOR_MIN_BITS).
const MinBits = 3

// MaxAlign is the alignment from which an arena position takes a block of
// the largest pool (MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN).
const MaxAlign = 1 << 21

const (
	minSize         = 1 << MinBits
	maxBits         = MinBits + PoolCount - 1
	maxSizeInternal = 1 << maxBits
)

// MaxSize is the largest block Alloc serves.
const MaxSize = maxSizeInternal - 2*RedZone

// Init resets an allocator to hold no blocks.
func Init(allocator *layout.BlockAllocator) {
	for idx := range allocator.Pools {
		pool := &allocator.Pools[idx]
		pool.Allocate = 0
		pool.Free = 0
		pool.Borrow = 0
		pool.FreeList.Set(nil)
	}
	allocator.NotEmptyMask = 0
	allocator.Lock.Locked = 0
}

// poolSize returns the block size of a pool.
func poolSize(pool int) uintptr {
	return 1 << (MinBits + pool)
}

// poolIndex returns the pool of the smallest blocks that hold size bytes.
func poolIndex(size uintptr) int {
	if size < minSize {
		return 0
	}
	return bits.Len64(uint64((size<<1)-1)>>MinBits) - 1
}

// link returns the word a free block links the next free block through.
func link(block unsafe.Pointer) *shm.Ptr[byte] {
	return (*shm.Ptr[byte])(block)
}

// take removes the first block of a pool's free list.
func take(allocator *layout.BlockAllocator, pool int) unsafe.Pointer {
	entry := &allocator.Pools[pool]
	block := unsafe.Pointer(entry.FreeList.Get())
	unpoison(block, linkSize)
	entry.FreeList.Set(link(block).Get())
	poison(block, linkSize)
	entry.Allocate++
	entry.Free--
	if entry.Free == 0 {
		allocator.NotEmptyMask &^= 1 << pool
	}
	return block
}

// borrow splits a block of the next larger pool into two of this pool.
func borrow(allocator *layout.BlockAllocator, pool int) {
	data := take(allocator, pool+1)
	unpoison(data, linkSize)
	entry := &allocator.Pools[pool]
	next := unsafe.Add(data, poolSize(pool))
	unpoison(next, linkSize)
	link(next).Set(entry.FreeList.Get())
	link(data).Set((*byte)(next))
	entry.FreeList.Set((*byte)(data))
	allocator.Pools[pool+1].Borrow++
	entry.Free += 2
	allocator.NotEmptyMask |= 1 << pool
	poison(next, linkSize)
	poison(data, linkSize)
}

// Lock takes an allocator's lock for a series of AllocLocked, FreeLocked,
// ContextAllocLocked and ContextFreeLocked calls, as a whole build or
// release of a generation makes.
//
// The lock is not reentrant: until Unlock the holder calls no other
// function of this package on the allocator, which would take the lock
// again, and takes no other lock, since its holder may be waiting for
// this allocator.
func Lock(allocator *layout.BlockAllocator) {
	shm.LockSpin(&allocator.Lock.Locked)
}

// Unlock releases an allocator's lock Lock took.
func Unlock(allocator *layout.BlockAllocator) {
	shm.UnlockSpin(&allocator.Lock.Locked)
}

// Alloc returns a block of at least size bytes, or nil for a zero size,
// a size over MaxSize or no free block large enough.
//
// The block's bytes are left as they were.
func Alloc(allocator *layout.BlockAllocator, size uintptr) unsafe.Pointer {
	Lock(allocator)
	defer Unlock(allocator)
	return AllocLocked(allocator, size)
}

// AllocLocked is Alloc for a caller holding the allocator's lock.
func AllocLocked(allocator *layout.BlockAllocator, size uintptr) unsafe.Pointer {
	if size == 0 || size > MaxSize {
		return nil
	}
	size += 2 * RedZone
	pool := poolIndex(size)
	mask := allocator.NotEmptyMask >> pool
	if mask == 0 {
		return nil
	}
	for parent := pool + bits.TrailingZeros32(mask); parent > pool; {
		parent--
		borrow(allocator, parent)
	}
	memory := take(allocator, pool)
	unpoison(unsafe.Add(memory, RedZone), size-2*RedZone)
	return unsafe.Add(memory, RedZone)
}

// put adds a block to the head of its pool's free list and poisons it
// whole.
func put(allocator *layout.BlockAllocator, block unsafe.Pointer, size uintptr) {
	pool := poolIndex(size)
	entry := &allocator.Pools[pool]
	unpoison(block, linkSize)
	link(block).Set(entry.FreeList.Get())
	entry.FreeList.Set((*byte)(block))
	entry.Free++
	allocator.NotEmptyMask |= 1 << pool
	poison(block, size)
}

// Free returns a block Alloc gave; size must be the size it was asked
// for.
func Free(allocator *layout.BlockAllocator, block unsafe.Pointer, size uintptr) {
	if size == 0 {
		return
	}
	Lock(allocator)
	defer Unlock(allocator)
	FreeLocked(allocator, block, size)
}

// FreeLocked is Free for a caller holding the allocator's lock.
func FreeLocked(allocator *layout.BlockAllocator, block unsafe.Pointer, size uintptr) {
	if size == 0 {
		return
	}
	put(allocator, unsafe.Add(block, -RedZone), size+2*RedZone)
}

// PutArena gives the allocator size bytes at arena, cut into the largest
// blocks the alignment of each position allows.
//
// The arena is first rounded inwards to 8 bytes at both ends, as absolute
// addresses; an arena that holds no aligned 8 bytes gives nothing.
func PutArena(allocator *layout.BlockAllocator, arena unsafe.Pointer, size uintptr) {
	start := uintptr(arena)
	position := (start + 7) &^ 7
	end := (start + size) &^ 7

	Lock(allocator)
	defer Unlock(allocator)

	for position < end {
		align := uintptr(1) << bits.TrailingZeros64(uint64(position))
		blockSize := align
		if align >= MaxAlign {
			blockSize = maxSizeInternal
		}
		for position+blockSize > end {
			blockSize >>= 1
		}
		blockSize = min(blockSize, maxSizeInternal)
		put(allocator, unsafe.Add(arena, position-start), blockSize)
		position += blockSize
	}
}

// FreeSize returns the bytes the allocator's free lists hold.
func FreeSize(allocator *layout.BlockAllocator) uintptr {
	Lock(allocator)
	defer Unlock(allocator)
	total := uintptr(0)
	for idx := range allocator.Pools {
		total += uintptr(allocator.Pools[idx].Free) * poolSize(idx)
	}
	return total
}
