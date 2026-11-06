// The C memory the native classify tests of the SDK build classifiers in.
//
// An agent builds a configuration in a memory context over the block
// allocator of its mapping, whose routines are header-inline; the tests reach
// them through these. An arena is one aligned region, the mapping a test
// resolves its lookups through, and is never freed.
#include <stddef.h>
#include <stdlib.h>

#include "common/memory.h"
#include "common/memory_block.h"

struct yanet_sys_test_arena {
	struct block_allocator allocator;
	struct memory_context context;
	void *region;
};

// Opens an arena of the given size with a memory context over it; NULL when
// the heap is exhausted.
struct yanet_sys_test_arena *
yanet_sys_test_arena_new(size_t size) {
	struct yanet_sys_test_arena *arena = malloc(sizeof(*arena));
	if (arena == NULL) {
		return NULL;
	}
	if (posix_memalign(
		    &arena->region, MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN, size
	    ) != 0) {
		free(arena);
		return NULL;
	}
	block_allocator_init(&arena->allocator);
	block_allocator_put_arena(&arena->allocator, arena->region, size);
	memory_context_init(
		&arena->context, "classify tests", &arena->allocator
	);
	return arena;
}

// The first byte of the arena's region.
void *
yanet_sys_test_arena_base(struct yanet_sys_test_arena *arena) {
	return arena->region;
}

// The memory context that allocates from the arena.
struct memory_context *
yanet_sys_test_arena_context(struct yanet_sys_test_arena *arena) {
	return &arena->context;
}

// Opens a memory context over the arena at the given place, as a module
// header embeds one.
void
yanet_sys_test_arena_init_context(
	struct yanet_sys_test_arena *arena,
	struct memory_context *context,
	const char *name
) {
	memory_context_init(context, name, &arena->allocator);
}
