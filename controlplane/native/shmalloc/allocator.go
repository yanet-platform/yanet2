//go:build linux && (amd64 || arm64)

// Package shmalloc allocates YANET shared memory the way the C block
// allocator and memory contexts do, without cgo outside AddressSanitizer
// builds, which reach the sanitizer through it.
//
// Its free lists are the C ones: a Go process and a C process can
// allocate from one allocator and free each other's blocks. That makes
// the algorithm itself, not only the layout, part of the contract: block
// sizes, the split order, the link word of a free block and the carving
// of an arena are those of common/memory_block.h.
package shmalloc

import (
	"errors"
	"math/bits"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// ErrNoMemory marks shared memory a build or a publication ran out of;
// every native package's own exhaustion error wraps it.
var ErrNoMemory = errors.New("shared memory exhausted")

// PoolCount is the number of block sizes (MEMORY_BLOCK_ALLOCATOR_EXP),
// taken from the allocator's layout.
const PoolCount = len(layout.BlockAllocator{}.Pools)

// linkSize is the size of the link word a free block starts with.
const linkSize = unsafe.Sizeof(shm.Ptr[byte]{})

const (
	minBits         = 3
	minSize         = 1 << minBits
	maxBits         = minBits + PoolCount - 1
	maxSizeInternal = 1 << maxBits
	// maxAlign is the largest alignment an arena block keeps; at it,
	// blocks take the largest size.
	maxAlign = 1 << 21
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
	allocator.Lock.Locked = false
}

// poolSize returns the block size of a pool.
func poolSize(pool int) uintptr {
	return 1 << (minBits + pool)
}

// poolIndex returns the pool of the smallest blocks that hold size bytes.
func poolIndex(size uintptr) int {
	if size < minSize {
		return 0
	}
	return bits.Len64(uint64((size<<1)-1)>>minBits) - 1
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

// Lock takes an allocator's lock for a series of AllocLocked and
// FreeLocked calls, as a whole build or release of a generation makes.
//
// Until Unlock the holder must not wait for a lock whose holder may wait
// for this allocator, such as an instance's agent, generation or
// configuration lock; another allocator's lock or a process-local mutex
// is safe.
func Lock(allocator *layout.BlockAllocator) {
	shm.LockByte(&allocator.Lock.Locked)
}

// Unlock releases an allocator's lock Lock took.
func Unlock(allocator *layout.BlockAllocator) {
	shm.UnlockByte(&allocator.Lock.Locked)
}

// Alloc returns a block of at least size bytes, or nil when no free block
// is large enough. The block's bytes are left as they were.
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

// put adds a block to the head of its pool's free list; there is no
// merging of neighbours.
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

// Free returns a block of size bytes that Alloc gave; size must be the
// size it was asked for.
func Free(allocator *layout.BlockAllocator, block unsafe.Pointer, size uintptr) {
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
func PutArena(allocator *layout.BlockAllocator, arena unsafe.Pointer, size uintptr) {
	address := uintptr(arena)
	offset := (address+7)&^7 - address
	end := (address+size)&^7 - address

	shm.LockByte(&allocator.Lock.Locked)
	defer shm.UnlockByte(&allocator.Lock.Locked)

	for offset < end {
		align := uintptr(1) << bits.TrailingZeros64(uint64(address+offset))
		blockSize := align
		if align >= maxAlign {
			blockSize = maxSizeInternal
		}
		for offset+blockSize > end {
			blockSize >>= 1
		}
		blockSize = min(blockSize, maxSizeInternal)
		put(allocator, unsafe.Add(arena, offset), blockSize)
		offset += blockSize
	}
}

// FreeSize returns the bytes the allocator's free lists hold.
func FreeSize(allocator *layout.BlockAllocator) uintptr {
	shm.LockByte(&allocator.Lock.Locked)
	defer shm.UnlockByte(&allocator.Lock.Locked)
	total := uintptr(0)
	for idx := range allocator.Pools {
		total += uintptr(allocator.Pools[idx].Free) * poolSize(idx)
	}
	return total
}
