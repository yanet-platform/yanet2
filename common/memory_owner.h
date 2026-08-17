#pragma once

#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
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

// Defined in common/memory.h, included further down once the definitions
// below no longer depend on it being incomplete.
struct memory_context;

// Smallest arena a memory_owner borrows from its parent context.
#define MEMORY_OWNER_MIN_GRANULE ((size_t)64 * 1024)

// One arena borrowed from the parent context and fed into the owner's
// block allocator.
//
// data is a shared-memory offset pointer: assign it with SET_OFFSET_OF
// and read it back with ADDR_OF.
struct memory_arena {
	void *data;
	uint64_t size;
};

// A block_allocator that grows on demand by borrowing whole arenas from a
// parent memory context, and returns them all on teardown.
//
// parent_ctx and arenas are shared-memory offset pointers.
struct memory_owner {
	// Must stay the first field: grow_owner points back to the owner,
	// and the debug tripwire reaches the arenas through it.
	struct block_allocator allocator;

	struct memory_context *parent_ctx;

	uint64_t arena_count;
	uint64_t arena_capacity;
	struct memory_arena *arenas;

	char name[64];
};

// Prepares an empty owner bound to parent_ctx. The parent context must
// outlive the owner: arenas are borrowed from it lazily and returned by
// memory_owner_release_all.
static inline int
memory_owner_init(
	struct memory_owner *owner,
	struct memory_context *parent_ctx,
	const char *name
) {
	block_allocator_init(&owner->allocator);
	SET_OFFSET_OF(&owner->allocator.grow_owner, owner);

	SET_OFFSET_OF(&owner->parent_ctx, parent_ctx);

	owner->arena_count = 0;
	owner->arena_capacity = 0;
	SET_OFFSET_OF(&owner->arenas, NULL);

	(void)strtcpy(owner->name, name, sizeof(owner->name));

	return 0;
}

#if defined(YANET_DEBUG)
// Debug tripwire for memory_bfree: a block freed through a context bound
// to this allocator must lie inside one of the borrowed arenas. A foreign
// block would otherwise land on the in-band free lists and corrupt them
// silently, so abort loudly instead.
//
// Takes owner->allocator.lock — the lock the grow path appends under —
// for a consistent (arenas, arena_count) pair. The interval is the true
// parent block (arena pointer minus its red zone, grown to the
// pool-rounded block), which is what put_arena ingested.
static inline void
memory_owner_assert_block_owned(
	struct block_allocator *allocator, void *block
) {
	struct memory_owner *owner = ADDR_OF(&allocator->grow_owner);
	if (owner == NULL) {
		return;
	}

	spinlock_lock(&owner->allocator.lock);
	struct memory_arena *arenas = ADDR_OF(&owner->arenas);
	uint64_t arena_count = owner->arena_count;
	uint64_t owned = 0;
	for (uint64_t idx = 0; idx < arena_count; ++idx) {
		uintptr_t base =
			(uintptr_t)ADDR_OF(&arenas[idx].data) - ASAN_RED_ZONE;
		size_t block_size = block_allocator_pool_size(
			allocator,
			block_allocator_pool_index(
				allocator,
				(size_t)arenas[idx].size + 2 * ASAN_RED_ZONE
			)
		);
		if ((uintptr_t)block >= base &&
		    (uintptr_t)block < base + block_size) {
			owned = 1;
			break;
		}
	}
	spinlock_unlock(&owner->allocator.lock);

	if (!owned) {
		fprintf(stderr,
			"yanet: memory_owner '%s': block %p freed through the "
			"owner-bound context belongs to a foreign allocator\n",
			owner->name,
			block);
		abort();
	}
}
#endif // defined(YANET_DEBUG)

// common/memory.h defines struct memory_context and the context-level
// allocation helpers the functions below call. It includes this header
// back at its end; the include cycle is closed on both orders because
// everything above only needs struct memory_context as an incomplete
// type.
#include "memory.h"

// Growth path for block_allocator_balloc: borrow one power-of-2 granule
// of at least `need` bytes from the parent context, track it, and feed it
// to the owner's allocator.
static inline int
memory_owner_grow(
	struct block_allocator *alloc, size_t need, struct memory_owner *owner
) {
	// The grow binding is identity-bound: a foreign allocator would
	// ingest the granule into the owner while the caller retries the
	// allocator, stranding it.
	assert(alloc == &owner->allocator);

	struct memory_context *parent_ctx = ADDR_OF(&owner->parent_ctx);

	// The parent block class to borrow: the requesting pool's block,
	// floored at the minimum granule. `need` already carries the child
	// allocator's red zones, so asking the parent for the block minus
	// its red-zone pair lands the parent's own addition back in the
	// same class — requesting `wanted` itself would round one pool up
	// and double-reserve under sanitizers.
	size_t wanted = block_allocator_pool_size(
		alloc, block_allocator_pool_index(alloc, need)
	);
	if (wanted < MEMORY_OWNER_MIN_GRANULE) {
		wanted = MEMORY_OWNER_MIN_GRANULE;
	}
	size_t granule = wanted - 2 * ASAN_RED_ZONE;

	// A power-of-2 block freshly borrowed from the buddy parent is
	// size-aligned, so put_arena yields exactly one whole block with
	// zero split waste. Under ASAN the returned pointer is shifted by
	// the red zone, so realign to the true block start and its
	// pool-rounded size — the exact same expression when red zones are
	// zero.
	size_t block = block_allocator_pool_size(
		alloc,
		block_allocator_pool_index(alloc, granule + 2 * ASAN_RED_ZONE)
	);

	// Serialize the whole growth — borrow, track, install — on the
	// owner lock: a competing miss whose own borrow fails then blocks
	// here for its final rescan and cannot observe exhaustion between
	// this granule's commitment and its installation. Nesting owner ->
	// parent inside this lock is safe: no path takes them in the
	// reverse order.
	spinlock_lock(&owner->allocator.lock);

	// A competitor that grew while we were missing may already have
	// installed an arena covering this request — the caller's rescan
	// finds it, so return without borrowing and keep surplus granules
	// out of the parent.
	if (alloc->not_empty_mask >> block_allocator_pool_index(alloc, need) !=
	    0) {
		spinlock_unlock(&owner->allocator.lock);
		return 0;
	}

	void *arena = memory_balloc(parent_ctx, granule);
	if (arena == NULL) {
		spinlock_unlock(&owner->allocator.lock);
		return -1;
	}

	if (owner->arena_count == owner->arena_capacity) {
		uint64_t capacity = owner->arena_capacity * 2;
		if (capacity == 0) {
			capacity = 4;
		}

		// The array cannot go through memory_brealloc: its data
		// fields are offsets relative to their own slot, so a memcpy
		// to a new address silently rebases every entry. Allocate
		// fresh and re-stamp each offset, like the agent registry
		// rebuild does.
		struct memory_arena *old_arenas = ADDR_OF(&owner->arenas);
		size_t old_bytes =
			owner->arena_capacity * sizeof(struct memory_arena);
		struct memory_arena *arenas =
			memory_balloc(parent_ctx, capacity * sizeof(*arenas));
		if (arenas == NULL) {
			memory_bfree(parent_ctx, arena, granule);
			spinlock_unlock(&owner->allocator.lock);
			return -1;
		}
		for (uint64_t idx = 0; idx < owner->arena_count; ++idx) {
			SET_OFFSET_OF(
				&arenas[idx].data,
				ADDR_OF(&old_arenas[idx].data)
			);
			arenas[idx].size = old_arenas[idx].size;
		}
		if (old_arenas != NULL) {
			memory_bfree(parent_ctx, old_arenas, old_bytes);
		}

		SET_OFFSET_OF(&owner->arenas, arenas);
		owner->arena_capacity = capacity;
	}

	struct memory_arena *arenas = ADDR_OF(&owner->arenas);
	SET_OFFSET_OF(&arenas[owner->arena_count].data, arena);
	arenas[owner->arena_count].size = granule;
	owner->arena_count++;

	block_allocator_put_arena_locked(
		&owner->allocator, (char *)arena - ASAN_RED_ZONE, block
	);

	spinlock_unlock(&owner->allocator.lock);

	return 0;
}

// Returns every borrowed arena to the parent context and frees the
// tracking array, leaving the owner ready for a fresh memory_owner_init;
// the caller frees the owner header itself. Teardown-only: quiesce
// allocations through the owner first.
//
// Quiescence is the operative guarantee — the lock only serializes the
// walk with a grow that already holds it, and a grow paused before
// acquiring can still append after the walk. Nested parent frees keep
// grow's owner -> parent order. The pools need no drain — their
// linkage lives in-band inside the released arenas.
static inline void
memory_owner_release_all(struct memory_owner *owner) {
	struct memory_context *parent_ctx = ADDR_OF(&owner->parent_ctx);

	spinlock_lock(&owner->allocator.lock);

	struct memory_arena *arenas = ADDR_OF(&owner->arenas);
	if (arenas != NULL) {
		for (uint64_t idx = 0; idx < owner->arena_count; ++idx) {
			memory_bfree(
				parent_ctx,
				ADDR_OF(&arenas[idx].data),
				arenas[idx].size
			);
		}
		memory_bfree(
			parent_ctx,
			arenas,
			owner->arena_capacity * sizeof(struct memory_arena)
		);
	}

	owner->arena_count = 0;
	owner->arena_capacity = 0;
	SET_OFFSET_OF(&owner->arenas, NULL);

	spinlock_unlock(&owner->allocator.lock);
}
