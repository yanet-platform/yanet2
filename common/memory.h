#pragma once

#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

#define BLOCK_COUNT 8

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

	context->block_allocator = block_allocator;
	snprintf(context->name, sizeof(context->name), "%s", name);

	return 0;
}

static inline void *
memory_balloc(struct memory_context *context, size_t size) {
	++context->balloc_count;
	context->balloc_size += size;
	return block_allocator_balloc(context->block_allocator, size);
}

static inline void
memory_bfree(struct memory_context *context, void *block, size_t size) {
	++context->bfree_count;
	context->bfree_size += size;
	return block_allocator_bfree(context->block_allocator, block, size);
}
