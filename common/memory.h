#pragma once

#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "memory_address.h"
#include "memory_block.h"
#include "strutils.h"

// The meson-generated header is absent for cgo compilation of Go packages
// reaching this file, which runs without a configured build directory.
// Only YANET_DEBUG is consumed below, so its absence just leaves the
// wrong-allocator tripwire disabled, matching a release configuration.
#if defined(__has_include)
#if __has_include("yanet_build_config.h")
#include "yanet_build_config.h"
#endif
#else
#include "yanet_build_config.h"
#endif

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
	// A NULL parent marks a root context, e.g. an agent, the dataplane
	// bootstrap context, or the cp_config bootstrap context.
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

	// A root context has no parent and no children yet.
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

	// No NULL guard here, unlike fini: by contract parent is always a live,
	// already-initialised context, so its block_allocator is never NULL. A
	// zeroed (finalised) parent has no valid allocator for us to inherit,
	// and no caller constructs a child from a finalised parent.
	struct block_allocator *allocator = ADDR_OF(&parent->block_allocator);
	SET_OFFSET_OF(&context->block_allocator, allocator);
	(void)strtcpy(context->name, name, sizeof(context->name));

	// Remember which context contains us, needed to unlink on teardown.
	SET_OFFSET_OF(&context->parent, parent);
	SET_OFFSET_OF(&context->first_child, NULL);

	// The splice is a link on the parent node, so the parent's
	// allocator lock guards it — even when the child itself goes on to
	// allocate through a different, owner-bound allocator.
	spinlock_lock(&allocator->lock);

	// Insert at the head of the parent's child list. Capture the current
	// head into next_sibling before overwriting first_child so the ordering
	// is correct.
	EQUATE_OFFSET(&context->next_sibling, &parent->first_child);
	SET_OFFSET_OF(&parent->first_child, context);

	spinlock_unlock(&allocator->lock);

	return 0;
}

// Reset a memory_context to the zero-init state.
//
// Unlinks the context from its parent's child list, then detaches any
// remaining children so finalising a parent before its children can never
// leave a child pointing at freed memory. Idempotent and order-independent.
static inline void
memory_context_unlink_from_parent(
	struct memory_context *self, struct memory_context *parent
) {
	// Walk the sibling chain and bridge over ourselves.
	struct memory_context **cursor = &parent->first_child;
	for (;;) {
		struct memory_context *child = ADDR_OF(cursor);
		if (child == NULL) {
			break;
		}
		if (child == self) {
			EQUATE_OFFSET(cursor, &self->next_sibling);
			break;
		}
		cursor = &child->next_sibling;
	}
}

// Detach any remaining children so their parent link never dangles after
// our own storage is reused.
static inline void
memory_context_detach_children(struct memory_context *self) {
	struct memory_context *child = ADDR_OF(&self->first_child);
	while (child != NULL) {
		struct memory_context *next = ADDR_OF(&child->next_sibling);
		SET_OFFSET_OF(&child->parent, NULL);
		SET_OFFSET_OF(&child->next_sibling, NULL);
		child = next;
	}
}

static inline void
memory_context_fini(struct memory_context *self) {
	// A zeroed offset reads back as NULL through ADDR_OF. A context that
	// was already fini'd has no tree links left to touch. Return before
	// taking a lock on a garbage allocator.
	struct block_allocator *allocator = ADDR_OF(&self->block_allocator);
	if (allocator == NULL) {
		return;
	}

	// The parent must stay alive across this call: reading self->parent
	// unlocked relies on no concurrent fini of the parent itself, whose
	// detach walk is what would rewrite this field.
	struct memory_context *parent = ADDR_OF(&self->parent);

	if (parent == NULL) {
		spinlock_lock(&allocator->lock);
		memory_context_detach_children(self);
		spinlock_unlock(&allocator->lock);
	} else {
		// Tree links hang off the parent node, so the unlink needs
		// the PARENT's allocator lock. With per-owner allocators the
		// child's own lock no longer equals it, while our child list
		// is still guarded by our own allocator, hence two sections
		// whenever the allocators differ. Sequential, never nested:
		// no path takes these two locks in opposite orders.
		struct block_allocator *parent_allocator =
			ADDR_OF(&parent->block_allocator);

		if (parent_allocator == allocator) {
			spinlock_lock(&allocator->lock);
			memory_context_unlink_from_parent(self, parent);
			memory_context_detach_children(self);
			spinlock_unlock(&allocator->lock);
		} else {
			spinlock_lock(&parent_allocator->lock);
			memory_context_unlink_from_parent(self, parent);
			spinlock_unlock(&parent_allocator->lock);

			spinlock_lock(&allocator->lock);
			memory_context_detach_children(self);
			spinlock_unlock(&allocator->lock);
		}
	}

	memset(self, 0, sizeof(*self));
}

static inline void *
memory_balloc(struct memory_context *context, size_t size) {
	void *result = block_allocator_balloc(
		ADDR_OF(&context->block_allocator), size
	);
	if (result == NULL) {
		return NULL;
	}
	// A single memory_context (e.g. an agent's own context) can now be
	// ballocked from by several controlplane threads at once.
	__atomic_fetch_add(&context->balloc_count, 1, __ATOMIC_RELAXED);
	__atomic_fetch_add(&context->balloc_size, size, __ATOMIC_RELAXED);
	return result;
}

#if defined(YANET_DEBUG)
// Debug wrong-allocator tripwire. Declared here so memory_bfree can call
// it ahead of the definition; struct memory_owner needs struct
// memory_context, so the definition lives in common/memory_owner.h, which
// this header pulls in at the very bottom.
static inline void
memory_owner_assert_block_owned(struct block_allocator *allocator, void *block);
#endif

static inline void
memory_bfree(struct memory_context *context, void *block, size_t size) {
	if (block == NULL || !size) {
		return;
	}
#if defined(YANET_DEBUG)
	// Freeing a foreign block through an owner-bound context corrupts
	// the in-band free lists silently, so catch it while it is still
	// cheap to name the culprit.
	memory_owner_assert_block_owned(
		ADDR_OF(&context->block_allocator), block
	);
#endif
// Verified reproducing: gcc (Ubuntu 13.3.0-6ubuntu2~24.04) 13.3.0, release
// build flags (-O2 -g -Wall -Wextra -Werror -march=haswell -fPIC), e.g.
// modules/acl/api/controlplane.c inlining memory_bfree into
// acl_module_config_free. context is derived from a relative offset
// pointer (ADDR_OF) at every caller. GCC compiles the theoretical,
// architecturally-unreachable branch where the stored offset is exactly 0
// (ADDR_OF's own NULL case), folds it into a literal near-null address,
// and then misreports the atomic RMW below as an overflowing write to a
// size-0 object at that address. Clang does not know -Wstringop-overflow,
// so the pragma is gcc-only.
#if defined(__GNUC__) && !defined(__clang__)
#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wstringop-overflow"
#endif
	__atomic_fetch_add(&context->bfree_count, 1, __ATOMIC_RELAXED);
	__atomic_fetch_add(&context->bfree_size, size, __ATOMIC_RELAXED);
#if defined(__GNUC__) && !defined(__clang__)
#pragma GCC diagnostic pop
#endif
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
	if (!new_size && !old_size) {
		return NULL;
	}

	if (!new_size) {
		memory_bfree(context, data, old_size);
		return NULL;
	}

	void *new_data = memory_balloc(context, new_size);
	if (new_data == NULL) {
		return NULL;
	}
	if (old_size < new_size) {
		if (old_size) {
			memcpy(new_data, data, old_size);
		}
	} else {
		memcpy(new_data, data, new_size);
	}

	if (old_size) {
		memory_bfree(context, data, old_size);
	}
	return new_data;
}

// Splits the include cycle with common/memory_owner.h: the owner struct
// and its non-context-touching helpers sit above memory.h's needs, while
// its grow/release helpers need struct memory_context from this header.
// Including it here guarantees every user of memory_bfree also sees the
// tripwire definition.
#include "memory_owner.h"
