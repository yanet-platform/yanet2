// Test-only C helpers: a self-contained shared-memory arena and the C LPM
// builder and lookup.
//
// The arena mimics an agent arena: one allocation that starts with the
// block allocator and its root memory context, so a byte copy of the whole
// arena is a relocatable image. Nothing in the module path references
// these symbols, so the linker keeps them out of module shared objects.
#pragma once

#include <stddef.h>
#include <stdint.h>

struct lpm;
struct yanet_sys_test_arena;

struct yanet_sys_test_arena *
yanet_sys_test_arena_new(size_t size);

void
yanet_sys_test_arena_free(struct yanet_sys_test_arena *arena);

// First byte of the arena; the whole image is [base, base + size).
uint8_t *
yanet_sys_test_arena_base(struct yanet_sys_test_arena *arena);

size_t
yanet_sys_test_arena_size(struct yanet_sys_test_arena *arena);

// Allocates a block of the given size from the arena, NULL when exhausted.
void *
yanet_sys_test_arena_alloc(struct yanet_sys_test_arena *arena, size_t size);

// Allocates and initialises an empty LPM inside the arena.
struct lpm *
yanet_sys_test_lpm_new(struct yanet_sys_test_arena *arena);

// Initialises an LPM embedded in a caller-owned arena block.
int
yanet_sys_test_lpm_init(struct yanet_sys_test_arena *arena, struct lpm *lpm);

int
yanet_sys_test_lpm_insert(
	struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value
);

// Releases every block of an initialised LPM.
void
yanet_sys_test_lpm_free(struct lpm *lpm);

uint32_t
yanet_sys_test_lpm_lookup(
	const struct lpm *lpm, uint8_t key_size, const uint8_t *key
);

// Looks up count consecutive keys, writing one result per key; the inline
// C lookup runs in a loop, for benchmarks.
void
yanet_sys_test_lpm_lookup_many(
	const struct lpm *lpm,
	uint8_t key_size,
	const uint8_t *keys,
	uint32_t *results,
	size_t count
);
