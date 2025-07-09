#include "common/radix.h"

#include <assert.h>
#include <stdio.h>

struct radix_iterate_ctx {
	uint8_t keys[4][10];
	uint32_t values[10];
	uint32_t count;
};

int
radix_iterate_cb(
	uint8_t key_size, const uint8_t *key, uint32_t value, void *data
) {
	assert(key_size == 4);
	struct radix_iterate_ctx *ctx = (struct radix_iterate_ctx *)data;
	memcpy(ctx->keys[ctx->count], key, 4);
	ctx->values[ctx->count] = value;
	++ctx->count;
	return 0;
}

int
main() {
	void *arena0 = malloc(1 << 24); // 16MB
	if (arena0 == NULL) {
		return 1;
	}

	struct block_allocator alloc;
	block_allocator_init(&alloc);
	block_allocator_put_arena(&alloc, arena0, 1 << 24);

	struct memory_context mem_ctx;
	if (memory_context_init(&mem_ctx, "test", &alloc) < 0) {
		return 1;
	}

	struct radix radix;
	if (radix_init(&radix, &mem_ctx) < 0) {
		return 1;
	}

	uint8_t k1[4] = {192, 168, 0, 1};
	uint8_t k2[4] = {192, 173, 255, 0};
	if (radix_insert(&radix, 4, k1, 1) < 0) {
		return 1;
	}
	if (radix_insert(&radix, 4, k2, 2) < 0) {
		return 1;
	}

	uint32_t v1 = radix_lookup(&radix, 4, k1);
	assert(v1 == 1);

	uint32_t v2 = radix_lookup(&radix, 4, k2);
	assert(v2 == 2);

	if (radix_insert(&radix, 4, k1, 3) < 0) {
		return 1;
	}
	v1 = radix_lookup(&radix, 4, k1);
	assert(v1 == 3);

	struct radix_iterate_ctx ctx;
	ctx.count = 0;
	if (radix_walk(&radix, 4, radix_iterate_cb, &ctx) < 0) {
		return 1;
	}
	assert(ctx.count == 2);

	assert(memcmp(k1, ctx.keys[0], 4) == 0);
	assert(ctx.values[0] == 3);

	assert(memcmp(k2, ctx.keys[1], 4) == 0);
	assert(ctx.values[1] == 2);

	radix_free(&radix);

	puts("OK!");
	return 0;
}