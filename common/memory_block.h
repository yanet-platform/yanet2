#pragma once

#include <stddef.h>
#include <stdint.h>

#define MEMORY_BLOCK_ALLOCATOR_MIN_BITS 3
#define MEMORY_BLOCK_ALLOCATOR_EXP 20
#define MEMORY_BLOCK_ALLOCATOR_MIN_SIZE (1 << MEMORY_BLOCK_ALLOCATOR_MIN_BITS)
#define MEMORY_BLOCK_ALLOCATOR_MAX_SIZE                                        \
	(1 << (MEMORY_BLOCK_ALLOCATOR_MIN_BITS + MEMORY_BLOCK_ALLOCATOR_EXP))

struct block_allocator_pool {
	uint64_t allocate;
	uint64_t free;
	uint64_t borrow;

	void *free_list;
};

typedef void *(*block_allocator_alloc_func)(size_t size, void *data);

struct block_allocator {
	block_allocator_alloc_func alloc_func;
	void *alloc_func_data;
	struct block_allocator_pool pools[MEMORY_BLOCK_ALLOCATOR_EXP];
};

static inline int
block_allocator_init(
	struct block_allocator *allocator,
	block_allocator_alloc_func alloc_func,
	void *alloc_func_data

) {
	allocator->alloc_func = alloc_func;
	allocator->alloc_func_data = alloc_func_data;

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

	size = (size << 1) - 1;
	size >>= MEMORY_BLOCK_ALLOCATOR_MIN_BITS;

	return sizeof(long long) * 8 - 1 - __builtin_clzll(size);
}

static inline void *
block_allocator_pool_get(
	struct block_allocator *allocator, struct block_allocator_pool *pool
) {
	(void)allocator;

	void *result = pool->free_list;
	pool->free_list = *(void **)pool->free_list;
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

	struct block_allocator_pool *pool = allocator->pools + pool_index;

	// Split the memory chunk into two piece and insert into free list
	size_t size = block_allocator_pool_size(allocator, pool_index);
	void *next_data = (void *)((uintptr_t)data + size);
	*(void **)data = next_data;
	*(void **)next_data = pool->free_list;
	pool->free_list = data;

	++parent_pool->borrow;
	pool->free += 2;
}

static inline void *
block_allocator_balloc(struct block_allocator *allocator, size_t size) {
	if (size < MEMORY_BLOCK_ALLOCATOR_MIN_SIZE)
		size = MEMORY_BLOCK_ALLOCATOR_MIN_SIZE;

	if (size > MEMORY_BLOCK_ALLOCATOR_MAX_SIZE)
		return NULL;

	size_t pool_index = block_allocator_pool_index(allocator, size);

	struct block_allocator_pool *pool = allocator->pools + pool_index;

	if (pool->free_list == NULL) {
		/*
		 * Look for the first parent pool with free memory block
		 * available and then recursively borrow memory block.
		 */
		size_t parent_pool_index = pool_index + 1;
		while (parent_pool_index < MEMORY_BLOCK_ALLOCATOR_EXP &&
		       allocator->pools[parent_pool_index].free_list == NULL) {
			++parent_pool_index;
		}

		if (parent_pool_index == MEMORY_BLOCK_ALLOCATOR_EXP) {
			size_t alloc_size = block_allocator_pool_size(
				allocator, parent_pool_index - 1
			);

			void *data = allocator->alloc_func(
				alloc_size, allocator->alloc_func_data
			);
			if (data == NULL)
				return NULL;

			struct block_allocator_pool *root =
				allocator->pools + MEMORY_BLOCK_ALLOCATOR_EXP -
				1;
			++root->free;
			root->free_list = data;
			*(void **)data = NULL;
			--parent_pool_index;
		}

		while (parent_pool_index-- > pool_index) {
			block_allocator_pool_borrow(
				allocator, parent_pool_index
			);
		}
	}

	return block_allocator_pool_get(allocator, pool);
}

static inline void
block_allocator_bfree(
	struct block_allocator *allocator, void *block, size_t size
) {
	size_t pool_index = block_allocator_pool_index(allocator, size);
	struct block_allocator_pool *pool = allocator->pools + pool_index;

	*(void **)block = pool->free_list;
	pool->free_list = block;
	++pool->free;
}
