#pragma once

#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "memory_address.h"
#include "memory_block.h"
#include "strutils.h"

struct memory_context {
	struct block_allocator *block_allocator;
	size_t balloc_count;
	size_t bfree_count;
	size_t balloc_size;
	size_t bfree_size;

	char name[64];

	// Tree links for per-subsystem memory diagnostics.
	//
	// All three are shared-memory offset pointers. Use ADDR_OF and
	// SET_OFFSET_OF to dereference or assign them.
	//
	// A NULL parent marks a root context (agent, dataplane bootstrap,
	// cp_config bootstrap).
	struct memory_context *parent;
	struct memory_context *first_child;
	struct memory_context *next_sibling;
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
	(void)strtcpy(context->name, name, sizeof(context->name));

	// Root context has no parent and no children yet.
	SET_OFFSET_OF(&context->parent, NULL);
	SET_OFFSET_OF(&context->first_child, NULL);
	SET_OFFSET_OF(&context->next_sibling, NULL);

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
	(void)strtcpy(context->name, name, sizeof(context->name));

	// Remember which context contains us.
	//
	// Required for unlink on teardown.
	SET_OFFSET_OF(&context->parent, parent);
	SET_OFFSET_OF(&context->first_child, NULL);

	// Insert at the head of the parent's child list.
	//
	// Write next_sibling before updating parent->first_child so a
	// concurrent reader always sees a consistent chain.
	EQUATE_OFFSET(&context->next_sibling, &parent->first_child);
	SET_OFFSET_OF(&parent->first_child, context);

	return 0;
}

// Remove this context from its parent's child list.
//
// Safe to call multiple times on the same context (idempotent).
//
// No-op when parent is NULL.
static inline void
memory_context_fini(struct memory_context *context) {
	struct memory_context *parent = ADDR_OF(&context->parent);
	if (parent == NULL) {
		return;
	}

	// Walk the sibling chain to find and unlink ourselves.
	struct memory_context **cursor = &parent->first_child;
	for (;;) {
		struct memory_context *child = ADDR_OF(cursor);
		if (child == NULL) {
			// Not found — already unlinked or never linked.
			break;
		}
		if (child == context) {
			// Bridge over ourselves: predecessor now points at our
			// next sibling.
			EQUATE_OFFSET(cursor, &context->next_sibling);
			break;
		}
		cursor = &child->next_sibling;
	}

	// Clear parent so repeat calls become no-ops.
	SET_OFFSET_OF(&context->parent, NULL);
	SET_OFFSET_OF(&context->next_sibling, NULL);
}

static inline void *
memory_balloc(struct memory_context *context, size_t size) {
	void *result = block_allocator_balloc(
		ADDR_OF(&context->block_allocator), size
	);
	if (result == NULL)
		return NULL;
	++context->balloc_count;
	context->balloc_size += size;
	return result;
}

static inline void
memory_bfree(struct memory_context *context, void *block, size_t size) {
	if (!size)
		return;
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
	if (old_size < new_size) {
		if (old_size)
			memcpy(new_data, data, old_size);
	} else {
		memcpy(new_data, data, new_size);
	}

	if (old_size)
		memory_bfree(context, data, old_size);
	return new_data;
}
