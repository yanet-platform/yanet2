//go:build linux && (amd64 || arm64) && asan

package nativetest

/*
#include <stddef.h>
#include <stdint.h>

#include <sanitizer/asan_interface.h>

// Writes the runs of [begin, begin + size) this process's sanitizer holds
// unaddressable, as offset from begin and size pairs, at most capacity of
// them, and returns how many there are.
__attribute__((no_sanitize("address"))) static size_t
nativetest_unaddressable_runs(
	uintptr_t begin, size_t size, uint64_t *runs, size_t capacity
) {
	size_t scale;
	size_t offset;
	__asan_get_shadow_mapping(&scale, &offset);
	const int8_t *shadow = (const int8_t *)((begin >> scale) + offset);
	size_t granules = (size + ((size_t)1 << scale) - 1) >> scale;
	size_t count = 0;
	int open = 0;
	size_t start = 0;
	for (size_t idx = 0; idx < granules;) {
		// Eight granules all addressable, or all not, keep the state.
		if (((uintptr_t)(shadow + idx) & 7) == 0 && idx + 8 <= granules) {
			uint64_t word = *(const uint64_t *)(shadow + idx);
			if ((!open && word == 0) ||
			    (open && (word & 0x8080808080808080ull) ==
					     0x8080808080808080ull)) {
				idx += 8;
				continue;
			}
		}
		int8_t value = shadow[idx];
		size_t at = idx << scale;
		// A positive value is the count of addressable leading bytes.
		if (value >= 0 && open) {
			if (count < capacity) {
				runs[2 * count] = start;
				runs[2 * count + 1] = at - start;
			}
			++count;
			open = 0;
		}
		if (value > 0 || (value < 0 && !open)) {
			open = 1;
			start = at + (value > 0 ? (size_t)value : 0);
		}
		++idx;
	}
	if (open) {
		if (count < capacity) {
			runs[2 * count] = start;
			runs[2 * count + 1] = size - start;
		}
		++count;
	}
	return count;
}
*/
import "C"

import (
	"errors"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// sanitized tells that this process's AddressSanitizer holds marks of the
// storage the helper is sent before each delivery and round.
const sanitized = true

// freeBlock is a block on a free list.
type freeBlock struct {
	address unsafe.Pointer
	size    uintptr
}

// errBrokenFreeList marks a free list that leaves the control-plane zone
// or holds another count of blocks than its pool records.
var errBrokenFreeList = errors.New("a free list of the control-plane zone is broken")

// Unaddressable reports whether this process's AddressSanitizer holds the
// byte at the address unaddressable.
func Unaddressable(address unsafe.Pointer) bool {
	return C.__asan_address_is_poisoned(address) != 0
}

// unpoison marks memory addressable to AddressSanitizer again.
func unpoison(memory []byte) {
	C.__asan_unpoison_memory_region(unsafe.Pointer(&memory[0]), C.size_t(len(memory)))
}

// markFreeBlocks marks the free blocks of the control-plane zone's
// allocator unaddressable in this process's shadow, so the blocks this
// process takes from them keep their red zones.
//
// It runs right after mapping, before any agent attaches: from then on
// this process is the zone's only allocator and its shadow holds every
// free block, which the helper is sent. A broken free list fails it.
func markFreeBlocks(cp *layout.CpConfig, memory []byte) error {
	start := uintptr(unsafe.Pointer(cp))
	end := uintptr(unsafe.Pointer(&memory[0])) + uintptr(len(memory))
	var blocks []freeBlock
	allocator := &cp.BlockAllocator
	shmalloc.Lock(allocator)
	for pool := range allocator.Pools {
		size := uintptr(1) << (shmalloc.MinBits + pool)
		block := unsafe.Pointer(allocator.Pools[pool].FreeList.Get())
		count := uint64(0)
		// The recorded count bounds the walk of a broken list.
		for ; block != nil && count < allocator.Pools[pool].Free; count++ {
			position := uintptr(block)
			if position < start || position > end || end-position < size {
				break
			}
			blocks = append(blocks, freeBlock{address: block, size: size})
			block = unsafe.Pointer((*shm.Ptr[byte])(block).Get())
		}
		if block != nil || count != allocator.Pools[pool].Free {
			shmalloc.Unlock(allocator)
			return errBrokenFreeList
		}
	}
	shmalloc.Unlock(allocator)
	for _, block := range blocks {
		C.__asan_poison_memory_region(block.address, C.size_t(block.size))
	}
	return nil
}

// unaddressableRuns returns the runs of the storage from the offset on
// that this process's shadow holds unaddressable, as storage offsets.
func unaddressableRuns(memory []byte, from uint64) []helperproto.Range {
	zone := memory[from:]
	// The first scan only counts the runs; the next makes room for them.
	var runs []C.uint64_t
	for {
		count := int(C.nativetest_unaddressable_runs(
			C.uintptr_t(uintptr(unsafe.Pointer(&zone[0]))),
			C.size_t(len(zone)),
			unsafe.SliceData(runs),
			C.size_t(len(runs)/2),
		))
		// The shadow may change between scans; a larger buffer is retried.
		if count > len(runs)/2 {
			runs = make([]C.uint64_t, 2*count)
			continue
		}
		ranges := make([]helperproto.Range, count)
		for idx := range ranges {
			ranges[idx] = helperproto.Range{Offset: from + uint64(runs[2*idx]), Size: uint64(runs[2*idx+1])}
		}
		return ranges
	}
}
