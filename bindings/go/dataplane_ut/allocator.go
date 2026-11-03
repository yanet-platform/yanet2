package dataplaneut

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "lib/controlplane/agent/agent.h"

// Keep the agent's free blocks in another allocator, whose existing pool
// operations also split held blocks when a test gives back a smaller one.
struct dataplane_ut_alloc_hold {
	struct block_allocator *allocator;
	struct block_allocator held;
};

// Blocks the allocator handed out of each pool so far, splits included.
static void
dataplane_ut_alloc_pool_allocations(struct agent *agent, uint64_t *out) {
	struct block_allocator *allocator = &agent->block_allocator;
	spinlock_lock(&allocator->lock);
	for (uint64_t pool = 0; pool < MEMORY_BLOCK_ALLOCATOR_EXP; ++pool) {
		out[pool] = allocator->pools[pool].allocate;
	}
	spinlock_unlock(&allocator->lock);
}

// Detach free lists without touching their blocks or allocation counters.
static struct dataplane_ut_alloc_hold *
dataplane_ut_alloc_take(struct agent *agent) {
	struct dataplane_ut_alloc_hold *hold = calloc(1, sizeof(*hold));
	if (hold == NULL) {
		return NULL;
	}
	struct block_allocator *allocator = &agent->block_allocator;
	hold->allocator = allocator;
	block_allocator_init(&hold->held);

	spinlock_lock(&allocator->lock);
	for (uint64_t pool = 0; pool < MEMORY_BLOCK_ALLOCATOR_EXP; ++pool) {
		struct block_allocator_pool *source = allocator->pools + pool;
		struct block_allocator_pool *target = hold->held.pools + pool;
		EQUATE_OFFSET(&target->free_list, &source->free_list);
		target->free = source->free;
		source->free_list = NULL;
		source->free = 0;
	}
	hold->held.not_empty_mask = allocator->not_empty_mask;
	allocator->not_empty_mask = 0;
	spinlock_unlock(&allocator->lock);
	return hold;
}

// Give back up to count blocks, splitting larger held blocks as needed.
// A negative fill preserves contents; the caller validates pool and count.
static uint64_t
dataplane_ut_alloc_give(
	struct dataplane_ut_alloc_hold *hold,
	uint64_t pool,
	uint64_t count,
	int fill
) {
	struct block_allocator *held = &hold->held;
	struct block_allocator *allocator = hold->allocator;
	size_t size = block_allocator_pool_size(held, pool);
	uint64_t given = 0;
	spinlock_lock(&allocator->lock);
	for (; given < count; ++given) {
		uint32_t mask = held->not_empty_mask >> pool;
		if (mask == 0) {
			break;
		}
		uint64_t source = pool + __builtin_ctz(mask);
		while (source > pool) {
			block_allocator_pool_borrow(held, --source);
		}
		void *block =
			block_allocator_pool_get(held, held->pools + pool);
		if (fill >= 0) {
			asan_unpoison_memory_region(block, size);
			memset(block, fill, size);
		}
		block_allocator_bfree_internal(allocator, block, size);
	}
	spinlock_unlock(&allocator->lock);
	return given;
}

// Return every remaining held block without changing the agent's allocation
// counters, then free the process-local hold.
static void
dataplane_ut_alloc_release(struct dataplane_ut_alloc_hold *hold) {
	for (uint64_t pool = 0; pool < MEMORY_BLOCK_ALLOCATOR_EXP; ++pool) {
		dataplane_ut_alloc_give(
			hold, pool, hold->held.pools[pool].free, -1
		);
	}
	free(hold);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// AllocatorPools is the number of block sizes of an agent's allocator: pool p
// holds blocks of 8 << p bytes, the allocator's red zones included.
const AllocatorPools = int(C.MEMORY_BLOCK_ALLOCATOR_EXP)

// AllocatorPoolSize returns the size in bytes of the blocks of pool p.
func AllocatorPoolSize(pool int) uint64 {
	if pool < 0 || pool >= AllocatorPools {
		panic("allocator pool out of range")
	}
	return uint64(C.MEMORY_BLOCK_ALLOCATOR_MIN_SIZE) << uint(pool)
}

// AllocatorPoolOf returns the pool a request of size bytes is served from.
//
// The size includes the red zones an ASan build adds on each side; a request
// larger than the largest block returns AllocatorPools.
func AllocatorPoolOf(size uint64) int {
	size += 2 * uint64(C.ASAN_RED_ZONE)
	for pool := range AllocatorPools {
		if size <= AllocatorPoolSize(pool) {
			return pool
		}
	}
	return AllocatorPools
}

// PoolAllocations returns, per pool, how many blocks the agent's allocator
// handed out of it since the agent was attached, those it split included.
func PoolAllocations(agent *ffi.Agent) []uint64 {
	counts := make([]uint64, AllocatorPools)
	C.dataplane_ut_alloc_pool_allocations(
		(*C.struct_agent)(agent.AsRawPtr()),
		(*C.uint64_t)(unsafe.Pointer(&counts[0])),
	)
	return counts
}

// AllocatorHold is a fault injection: a test's hold on every free block of
// an agent's allocator, so that the allocator serves only the blocks the test
// gives back and fails the first request they cannot serve.
//
// The allocator serves a request from the pool of its size or by splitting a
// block of the smallest larger pool that has one, and never merges freed
// blocks, so the blocks given back decide exactly which request fails. The
// hold is for one test on an agent no other goroutine uses until Release.
type AllocatorHold struct {
	hold *C.struct_dataplane_ut_alloc_hold
}

// HoldAllocator takes every free block of the agent's allocator into a hold:
// from then on every request of the agent fails until blocks are given back.
func HoldAllocator(agent *ffi.Agent) (*AllocatorHold, error) {
	hold := C.dataplane_ut_alloc_take((*C.struct_agent)(agent.AsRawPtr()))
	if hold == nil {
		return nil, errors.New("failed to allocate an allocator hold")
	}
	return &AllocatorHold{hold: hold}, nil
}

// Give hands blocks of a pool back to the allocator, each filled with a byte,
// so a request served from one finds bytes it did not write.
//
// A block is cut from a larger held block when the pool holds none, as the
// allocator splits one. When the held blocks run out part way, the blocks
// handed back so far stay with the allocator, and the error says how many.
func (m *AllocatorHold) Give(pool int, count int, fill byte) error {
	if pool < 0 || pool >= AllocatorPools || count < 0 {
		return fmt.Errorf("invalid allocator pool %d or block count %d", pool, count)
	}
	given := C.dataplane_ut_alloc_give(m.hold, C.uint64_t(pool), C.uint64_t(count), C.int(fill))
	if int(given) != count {
		return fmt.Errorf("no held block for block %d of pool %d", given, pool)
	}
	return nil
}

// Release hands every block still held back to the allocator; the hold is
// not used afterwards.
func (m *AllocatorHold) Release() {
	C.dataplane_ut_alloc_release(m.hold)
	m.hold = nil
}
