#pragma once

#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "memory_address.h"

#include "memory_block.h"

// TODO: link parent and child context
struct memory_context {
	struct block_allocator *block_allocator;
	size_t balloc_count;
	size_t bfree_count;
	size_t balloc_size;
	size_t bfree_size;

	char name[64];
};

static inline int
memory_context_init(
	struct memory_context *context,
	const char *name,
	struct block_allocator *block_allocator
) {
	context->balloc_count = 0;
	context->bfree_count = 0;
	context->balloc_size = 0;
	context->bfree_size = 0;

	SET_OFFSET_OF(&context->block_allocator, block_allocator);
	strncpy(context->name, name, sizeof(context->name) - 1);
	context->name[sizeof(context->name) - 1] = 0;

	return 0;
}

static inline int
memory_context_init_from(
	struct memory_context *context,
	struct memory_context *parent,
	const char *name
) {
	context->balloc_count = 0;
	context->bfree_count = 0;
	context->balloc_size = 0;
	context->bfree_size = 0;

	SET_OFFSET_OF(
		&context->block_allocator, ADDR_OF(&parent->block_allocator)
	);
	strncpy(context->name, name, sizeof(context->name) - 1);
	context->name[sizeof(context->name) - 1] = 0;

	return 0;
}

static inline void *
memory_balloc(struct memory_context *context, size_t size) {
	++context->balloc_count;
	context->balloc_size += size;
	return block_allocator_balloc(ADDR_OF(&context->block_allocator), size);
}

static inline void
memory_bfree(struct memory_context *context, void *block, size_t size) {
	++context->bfree_count;
	context->bfree_size += size;
	return block_allocator_bfree(
		ADDR_OF(&context->block_allocator), block, size
	);
}

static inline void *
memory_brealloc(
	struct memory_context *context,
	void *data,
	size_t old_size,
	size_t new_size
) {
	if (!new_size && !old_size)
		return NULL;

	if (!new_size) {
		memory_bfree(context, data, old_size);
		return NULL;
	}

	void *new_data = memory_balloc(context, new_size);
	if (new_data == NULL)
		return NULL;
	if (old_size < new_size)
		memcpy(new_data, data, old_size);
	else
		memcpy(new_data, data, new_size);

	if (old_size)
		memory_bfree(context, data, old_size);
	return new_data;
}
