#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#include <endian.h>

#define ARENA_SIZE (1 << 20)

static int
walk_func(
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value,
	void *check
) {
	(void)key_size;

	if (*(uint32_t *)(from + 11) != htobe32(value * 256)) {
		return -1;
	}
	if (from[15] != 4)
		return -1;

	if (*(uint32_t *)(to + 11) != htobe32(value * 256)) {
		return -1;
	}
	if (to[15] != 8)
		return -1;

	if (value != *(uint32_t *)check)
		return -1;

	++(*(uint32_t *)check);
	return 0;
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	void *arena0 = malloc(ARENA_SIZE);
	if (arena0 == NULL) {
		fprintf(stdout, "could not allocate arena0\n");
		return -1;
	}

	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena0, ARENA_SIZE);

	struct memory_context mctx;
	memory_context_init(&mctx, "lpm", &ba);

	struct lpm lpm;
	if (lpm_init(&lpm, &mctx)) {
		fprintf(stdout, "could not initialize lpm\n");
		return -1;
	}

	uint8_t from[16];
	memset(from, 0, 16);
	uint8_t to[16];
	memset(to, 0, 16);

	// Put each value into new page to get out of memory error
	uint32_t idx = 0;
	do {
		*(uint32_t *)(from + 11) = htobe32(idx * 256);
		from[15] = 4;
		*(uint32_t *)(to + 11) = htobe32(idx * 256);
		to[15] = 8;
		if (lpm_insert(&lpm, 16, from, to, idx))
			break;
		++idx;
	} while (1);
	uint32_t fail_idx = idx;

	// Check we do not fail after failed insert
	if (!lpm_insert(&lpm, 16, from, to, idx)) {
		fprintf(stdout, "insertion repeat should fail\n");
		return -1;
	}

	// Attach new arena to the block allocator
	void *arena1 = malloc(1 << 20);
	if (arena1 == NULL) {
		fprintf(stdout, "could not allocate arena1\n");
		return -1;
	}
	block_allocator_put_arena(&ba, arena1, ARENA_SIZE);

	// Check the lpm can allocate new pages after allocator space expansion
	do {
		*(uint32_t *)(from + 11) = htobe32(idx * 256);
		from[15] = 4;
		*(uint32_t *)(to + 11) = htobe32(idx * 256);
		to[15] = 8;
		if (lpm_insert(&lpm, 16, from, to, idx))
			break;
		++idx;
	} while (1);

	if (idx == fail_idx) {
		fprintf(stdout,
			"could not insert after allocator space expansion");
		return -1;
	}

	fail_idx = idx;
	idx = 0;
	memset(from, 0, 16);
	memset(to, 0xff, 16);
	if (lpm_walk(&lpm, 16, from, to, walk_func, &idx)) {
		fprintf(stdout, "walk verification failed\n");
		return -1;
	}
	if (idx != fail_idx) {
		fprintf(stdout, "invalid value count\n");
		return -1;
	}

	lpm_free(&lpm);

	if (mctx.balloc_size != mctx.bfree_size) {
		fprintf(stdout,
			"alloc and free sizes should be equal %lu != %lu\n",
			mctx.balloc_size,
			mctx.bfree_size);
		return -1;
	}

	free(arena1);
	free(arena0);
	return 0;
}
