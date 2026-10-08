#include "test_shim.h"

#include <stdlib.h>
#include <string.h>

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

// Arena header placed at the first byte of the arena.
//
// The allocator and the root context live inside the image, so their
// relative links stay valid when the image is copied elsewhere.
struct yanet_sys_test_arena {
	struct block_allocator allocator;
	struct memory_context context;
	size_t size;
};

#define TEST_ARENA_ALIGN ((size_t)1 << 21)
#define TEST_ARENA_HEADER ((size_t)1 << 16)

struct yanet_sys_test_arena *
yanet_sys_test_arena_new(size_t size) {
	if (size <= TEST_ARENA_HEADER || size % TEST_ARENA_ALIGN != 0) {
		return NULL;
	}
	struct yanet_sys_test_arena *arena =
		aligned_alloc(TEST_ARENA_ALIGN, size);
	if (arena == NULL) {
		return NULL;
	}
	memset(arena, 0, size);
	block_allocator_init(&arena->allocator);
	memory_context_init(&arena->context, "test-arena", &arena->allocator);
	arena->size = size;
	block_allocator_put_arena(
		&arena->allocator,
		(uint8_t *)arena + TEST_ARENA_HEADER,
		size - TEST_ARENA_HEADER
	);
	return arena;
}

void
yanet_sys_test_arena_free(struct yanet_sys_test_arena *arena) {
	free(arena);
}

uint8_t *
yanet_sys_test_arena_base(struct yanet_sys_test_arena *arena) {
	return (uint8_t *)arena;
}

size_t
yanet_sys_test_arena_size(struct yanet_sys_test_arena *arena) {
	return arena->size;
}

void *
yanet_sys_test_arena_alloc(struct yanet_sys_test_arena *arena, size_t size) {
	void *block = memory_balloc(&arena->context, size);
	if (block != NULL) {
		memset(block, 0, size);
	}
	return block;
}

int
yanet_sys_test_lpm_init(struct yanet_sys_test_arena *arena, struct lpm *lpm) {
	return lpm_init(lpm, &arena->context, "test-lpm");
}

struct lpm *
yanet_sys_test_lpm_new(struct yanet_sys_test_arena *arena) {
	struct lpm *lpm = yanet_sys_test_arena_alloc(arena, sizeof(struct lpm));
	if (lpm == NULL) {
		return NULL;
	}
	if (yanet_sys_test_lpm_init(arena, lpm) != 0) {
		return NULL;
	}
	return lpm;
}

int
yanet_sys_test_lpm_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
) {
	return lpm_insert(lpm, key_size, from, to, value);
}

void
yanet_sys_test_lpm_free(struct lpm *lpm) {
	lpm_free(lpm);
}

uint32_t
yanet_sys_test_lpm_lookup(
	const struct lpm *lpm, uint8_t key_size, const uint8_t *key
) {
	return lpm_lookup(lpm, key_size, key);
}

void
yanet_sys_test_lpm_lookup_many(
	const struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *keys,
	uint32_t *results,
	size_t count
) {
	for (size_t idx = 0; idx < count; ++idx) {
		results[idx] = lpm_lookup(lpm, key_size, keys + idx * key_size);
	}
}
