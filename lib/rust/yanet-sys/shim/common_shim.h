// Helpers over the C of `common/` that the SDK builds embedded objects with.
//
// One wrapper per C routine, header-inline in C, compiled into the shim
// library with hidden visibility as the dataplane shim is. The routines are
// those of the LPM and of the C memory a configuration is built in; the SDK's
// test owner sets up a C memory context over its memory with the second group.
#pragma once

#include <stddef.h>
#include <stdint.h>

#include "shim_visibility.h"

struct block_allocator;
struct lpm;
struct memory_context;

// lpm_init (common/lpm.h): opens an LPM at the given address as a child of
// the given memory context, with a name and its first page.
//
// Returns 0, or -1 with errno ENOMEM when the context's memory is exhausted.
// The LPM is then not usable but must still be passed to
// yanet_sys_common_lpm_free, which unlinks its context from the parent.
YANET_SYS_HIDDEN int
yanet_sys_common_lpm_init(
	struct lpm *lpm, struct memory_context *memory_context, const char *name
);

// lpm_insert (common/lpm.h): maps an inclusive big-endian range of keys of
// the given size to a value.
//
// Returns 0, or -1 with errno ENOMEM when a page cannot be allocated; the
// LPM then stays valid for yanet_sys_common_lpm_free.
YANET_SYS_HIDDEN int
yanet_sys_common_lpm_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
);

// lpm_free (common/lpm.h): returns every page of the LPM and unlinks its
// context from the parent.
//
// Cannot fail. Sound after a failed init or insert.
YANET_SYS_HIDDEN void
yanet_sys_common_lpm_free(struct lpm *lpm);

// block_allocator_init (common/memory_block.h): empties the allocator.
//
// Returns 0, always.
YANET_SYS_HIDDEN int
yanet_sys_common_block_allocator_init(struct block_allocator *allocator);

// block_allocator_put_arena (common/memory_block.h): hands the allocator a
// byte range of the given size to allocate from.
//
// The bytes are cut into blocks by their alignment, so the usable size
// depends on the arena's. Cannot fail.
YANET_SYS_HIDDEN void
yanet_sys_common_block_allocator_put_arena(
	struct block_allocator *allocator, void *arena, size_t size
);

// block_allocator_free_size (common/memory_block.h): the bytes the allocator
// holds free, which an allocation takes and a free returns whole.
YANET_SYS_HIDDEN size_t
yanet_sys_common_block_allocator_free_size(struct block_allocator *allocator);

// memory_context_init (common/memory.h): makes a root memory context with a
// name over a block allocator.
//
// Returns 0, always.
YANET_SYS_HIDDEN int
yanet_sys_common_memory_context_init(
	struct memory_context *context,
	const char *name,
	struct block_allocator *allocator
);
