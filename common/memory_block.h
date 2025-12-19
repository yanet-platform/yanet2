#pragma once

#include <assert.h>
#include <stddef.h>
#include <stdint.h>

#include "asan.h"

#include "memory_address.h"

#ifdef HAVE_ASAN
#define ASAN_RED_ZONE 32
#else
#define ASAN_RED_ZONE 0
#endif

static_assert(
	ASAN_RED_ZONE == 0 || ASAN_RED_ZONE >= 8, "invalid red zone size"
);

#define MEMORY_BLOCK_ALLOCATOR_EXP 24
#define MEMORY_BLOCK_ALLOCATOR_MIN_BITS 3
#define MEMORY_BLOCK_ALLOCATOR_MAX_BITS                                        \
	(MEMORY_BLOCK_ALLOCATOR_MIN_BITS + MEMORY_BLOCK_ALLOCATOR_EXP - 1)
// Based on the hugepage size (currently 2MB), we depend on this size when
// mapping shared memory. There could be tricky solutions to mmap with max block
// alignment, but those solutions seem brittle and cumbersome, so we decided to
// restrict allocator block alignment to the huge page size.
#define MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN (1 << 21)
#define MEMORY_BLOCK_ALLOCATOR_MIN_SIZE (1 << MEMORY_BLOCK_ALLOCATOR_MIN_BITS)
#define MEMORY_BLOCK_ALLOCATOR_MAX_SIZE_INTERNAL                               \
	(1 << MEMORY_BLOCK_ALLOCATOR_MAX_BITS)
#define MEMORY_BLOCK_ALLOCATOR_MAX_SIZE                                        \
	(MEMORY_BLOCK_ALLOCATOR_MAX_SIZE_INTERNAL - ASAN_RED_ZONE * 2)

struct block_allocator_pool {
	uint64_t allocate;
	uint64_t free;
	uint64_t borrow;

	void *free_list;
};

typedef void *(*block_allocator_alloc_func)(size_t size, void *data);

struct block_allocator {
	struct block_allocator_pool pools[MEMORY_BLOCK_ALLOCATOR_EXP];
};

// FIXME: the routine must accept block sizes
static inline int
block_allocator_init(struct block_allocator *allocator) {
	for (size_t pool_idx = 0; pool_idx < MEMORY_BLOCK_ALLOCATOR_EXP;
	     ++pool_idx) {
		allocator->pools[pool_idx].allocate = 0;
		allocator->pools[pool_idx].free = 0;
		allocator->pools[pool_idx].borrow = 0;
		allocator->pools[pool_idx].free_list = NULL;
	}

	return 0;
}

static inline size_t
block_allocator_pool_size(
	struct block_allocator *allocator, size_t pool_index
) {
	(void)allocator;

	return 1 << (MEMORY_BLOCK_ALLOCATOR_MIN_BITS + pool_index);
}

static inline size_t
block_allocator_pool_index(struct block_allocator *allocator, size_t size) {
	(void)allocator;

	if (size < MEMORY_BLOCK_ALLOCATOR_MIN_SIZE)
		return 0;

	// Make 'size' the upper end of its power-of-2 range
	size = (size << 1) - 1;
	// Normalize by the minimum block size.
	// This converts the transformed size into units relative to
	// MEMORY_BLOCK_ALLOCATOR_MIN_SIZE.
	size >>= MEMORY_BLOCK_ALLOCATOR_MIN_BITS;

	// Calculate floor(log2(n)), where n represents the relative uints.
	return sizeof(long long) * 8 - 1 - __builtin_clzll(size);
}

static inline void *
block_allocator_pool_get(
	struct block_allocator *allocator, struct block_allocator_pool *pool
) {
	(void)allocator;

	void *result = ADDR_OF(&pool->free_list);
	asan_unpoison_memory_region(result, sizeof(void *));
	SET_OFFSET_OF(&pool->free_list, ADDR_OF((void **)result));
	asan_poison_memory_region(result, sizeof(void *));

	++pool->allocate;
	--pool->free;

	return result;
}

static inline void
block_allocator_pool_borrow(
	struct block_allocator *allocator, size_t pool_index
) {
	// Get a memory chunk from parent pool
	struct block_allocator_pool *parent_pool =
		allocator->pools + pool_index + 1;

	void *data = block_allocator_pool_get(allocator, parent_pool);
	asan_unpoison_memory_region(data, sizeof(void *));

	struct block_allocator_pool *pool = allocator->pools + pool_index;

	// Split the memory chunk into two piece and insert into free list
	size_t size = block_allocator_pool_size(allocator, pool_index);
	void *next_data = (void *)((uintptr_t)data + size);
	asan_unpoison_memory_region(next_data, sizeof(void *));

	SET_OFFSET_OF((void **)next_data, ADDR_OF(&pool->free_list));
	SET_OFFSET_OF((void **)data, next_data);
	SET_OFFSET_OF(&pool->free_list, data);

	++parent_pool->borrow;
	pool->free += 2;

	asan_poison_memory_region(next_data, sizeof(void *));
	asan_poison_memory_region(data, sizeof(void *));
}

static inline void *
block_allocator_balloc(struct block_allocator *allocator, size_t size) {
	if (!size)
		return NULL;

	if (size > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE)
		return NULL;

	size += 2 * ASAN_RED_ZONE;

	size_t pool_index = block_allocator_pool_index(allocator, size);

	struct block_allocator_pool *pool = allocator->pools + pool_index;

	if (pool->free == 0) {
		/*
		 * Look for the first parent pool with free memory block
		 * available and then recursively borrow memory block.
		 */
		size_t parent_pool_index = pool_index + 1;
		while (parent_pool_index < MEMORY_BLOCK_ALLOCATOR_EXP &&
		       /*		       ADDR_OF(
					      allocator,
					      allocator->pools[parent_pool_index].free_list
				      ) == NULL) {
				      */
		       allocator->pools[parent_pool_index].free == 0) {
			++parent_pool_index;
		}

		if (parent_pool_index == MEMORY_BLOCK_ALLOCATOR_EXP) {
			return NULL;
			/*
					FIXME: not sure should a block
		   allocator try to seize new memory regions or not.
					*/
			/*
					size_t alloc_size =
		   block_allocator_pool_size( allocator,
		   parent_pool_index - 1
					);

					void *data =
		   allocator->alloc_func( alloc_size,
		   allocator->alloc_func_data
					);
					if (data == NULL)
						return NULL;

					struct block_allocator_pool
		   *root = allocator->pools + MEMORY_BLOCK_ALLOCATOR_EXP
		   - 1;
					++root->free;
					root->free_list = data;
					*(void **)data = NULL;
					--parent_pool_index;
		*/
		}

		while (parent_pool_index-- > pool_index) {
			block_allocator_pool_borrow(
				allocator, parent_pool_index
			);
		}
	}

	void *memory = block_allocator_pool_get(allocator, pool);
	asan_unpoison_memory_region(
		memory + ASAN_RED_ZONE, size - 2 * ASAN_RED_ZONE
	);

	return memory + ASAN_RED_ZONE;
}

static inline void
block_allocator_bfree_internal(
	struct block_allocator *allocator, void *block, size_t size
) {
	if (!size)
		return;

	size_t pool_index = block_allocator_pool_index(allocator, size);
	struct block_allocator_pool *pool = allocator->pools + pool_index;

	asan_unpoison_memory_region(block, sizeof(void *));
	SET_OFFSET_OF((void **)block, ADDR_OF(&pool->free_list));
	SET_OFFSET_OF(&pool->free_list, block);
	++pool->free;

	asan_poison_memory_region(block, size);
}

static inline void
block_allocator_bfree(
	struct block_allocator *allocator, void *block, size_t size
) {
	if (!size)
		return;

	size += 2 * ASAN_RED_ZONE;
	block -= ASAN_RED_ZONE;

	block_allocator_bfree_internal(allocator, block, size);
}

static inline void
block_allocator_put_arena(
	struct block_allocator *allocator, void *arena, size_t size
) {
	uintptr_t pos = (uintptr_t)arena;
	pos = (pos + 7) & ~(uintptr_t)0x07; // round up to 8 byte boundary
	uintptr_t end = (uintptr_t)arena + size;
	end = end & ~(uintptr_t)0x07; // round down to 8 byte boundary

	while (pos < end) {
		size_t align = (size_t)1 << __builtin_ctzll(pos);
		// If alignment is greater than or equal to
		// MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN, then we can allocate blocks
		// with their maximum size
		size_t block_size =
			align < MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN
				? align
				: MEMORY_BLOCK_ALLOCATOR_MAX_SIZE_INTERNAL;
		/*
		 * FIXME:
		 * The loop bellow could be replaced with some bit magic but
		 * let us do it in the future
		 */
		while (pos + block_size > end)
			block_size >>= 1;

		if (block_size > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE_INTERNAL)
			block_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE_INTERNAL;

		block_allocator_bfree_internal(
			allocator, (void *)pos, block_size
		);
		pos += block_size;
	}
}

static inline size_t
block_allocator_free_size(struct block_allocator *alloc) {
	size_t size = 0;
	for (size_t i = 0; i < MEMORY_BLOCK_ALLOCATOR_EXP; ++i) {
		struct block_allocator_pool *pool = &alloc->pools[i];
		size += pool->free * block_allocator_pool_size(alloc, i);
	}
	return size;
}
