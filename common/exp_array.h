#pragma once

#include "common/numutils.h"

#include "common/memory.h"

static inline int
mem_array_expand_exp(
	struct memory_context *memory_context,
	void **array,
	size_t item_size,
	uint64_t *count
) {
	if (!((*count - 1) & *count)) {
		uint64_t old_size = *count * item_size;
		uint64_t new_size = old_size * 2;
		if (!new_size) {
			// First one allocation: size is zero
			new_size = item_size;
		}
		void *new_array = memory_brealloc(
			memory_context, *array, old_size, new_size
		);
		if (!new_array)
			return -1;
		*array = new_array;
	}

	*count += 1;
	return 0;
}

static inline void
mem_array_free_exp(
	struct memory_context *memory_context,
	void *array,
	size_t item_size,
	uint64_t count
) {
	if (!count)
		return;

	uint64_t capacity = 1 << uint64_log(count);
	memory_bfree(memory_context, array, capacity * item_size);
}

static inline void *
mem_array_alloc_exp(
	struct memory_context *memory_context,
	size_t item_size,
	uint64_t count,
	uint64_t *res_capacity
) {
	if (!count) {
		if (res_capacity) {
			*res_capacity = 0;
		}
		return NULL;
	}
	uint64_t capacity = 1 << uint64_log(count);
	if (res_capacity) {
		*res_capacity = capacity;
	}
	return memory_balloc(memory_context, capacity * item_size);
}
