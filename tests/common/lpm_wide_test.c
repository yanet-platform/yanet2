#include "common/lpm_wide.h"
#include "common/memory.h"
#include "common/memory_block.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#include <endian.h>

#define ARENA_SIZE (1 << 24)

static uint32_t
read_u32(const uint8_t *p) {
	uint32_t v;
	memcpy(&v, p, sizeof(v));
	return v;
}

static void
write_u32(uint8_t *p, uint32_t v) {
	memcpy(p, &v, sizeof(v));
}

static int
walk_func(
	uint8_t key_size,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t value,
	void *check
) {
	(void)key_size;

	if (read_u32(from + 8) != htobe32(value * 256)) {
		return -1;
	}
	if (from[15] != 4)
		return -1;

	if (read_u32(to + 8) != htobe32(value * 256)) {
		return -1;
	}
	if (to[15] != 8)
		return -1;

	if (value != *(uint32_t *)check)
		return -1;

	++(*(uint32_t *)check);
	return 0;
}

// Broad prefix value must survive when a narrower overlapping prefix is
// inserted afterwards.
static int
test_overlapping_prefixes(void) {
	void *arena = malloc(ARENA_SIZE);
	if (arena == NULL)
		return -1;

	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, ARENA_SIZE);

	struct memory_context mctx;
	memory_context_init(&mctx, "lpm_overlap", &ba);

	struct lpm_wide tree;
	if (lpm_wide_init(&tree, &mctx)) {
		free(arena);
		return -1;
	}

	// 0.0.0.0/0 -> value 42, then 0.1.0.0/16 -> value 99.
	uint8_t from[4], to[4];
	memset(from, 0x00, 4);
	memset(to, 0xff, 4);
	lpm_wide_insert(&tree, 4, from, to, 42);
	from[1] = 0x01;
	to[0] = 0x00;
	to[1] = 0x01;
	lpm_wide_insert(&tree, 4, from, to, 99);

	int rc = -1;
	uint8_t k1[4] = {0x00, 0x01, 0x02, 0x03};
	uint8_t k2[4] = {0x00, 0x02, 0x00, 0x00};
	if (lpm_wide_lookup(&tree, 4, k1) != 99) {
		fprintf(stdout, "narrow prefix lookup failed\n");
	} else if (lpm_wide_lookup(&tree, 4, k2) != 42) {
		fprintf(stdout, "broad prefix lost after page split\n");
	} else {
		rc = 0;
	}

	lpm_wide_free(&tree);
	memory_context_fini(&mctx);
	free(arena);
	return rc;
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	if (test_overlapping_prefixes()) {
		fprintf(stdout, "test_overlapping_prefixes: FAILED\n");
		return -1;
	}

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

	struct lpm_wide lpm;
	if (lpm_wide_init(&lpm, &mctx)) {
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
		write_u32(from + 8, htobe32(idx * 256));
		from[15] = 4;
		write_u32(to + 8, htobe32(idx * 256));
		to[15] = 8;
		if (lpm_wide_insert(&lpm, 16, from, to, idx))
			break;
		++idx;
	} while (1);
	uint32_t fail_idx = idx;

	// Check we do not fail after failed insert
	if (!lpm_wide_insert(&lpm, 16, from, to, idx)) {
		fprintf(stdout, "insertion repeat should fail\n");
		return -1;
	}

	// Attach new arena to the block allocator
	void *arena1 = malloc(ARENA_SIZE);
	if (arena1 == NULL) {
		fprintf(stdout, "could not allocate arena1\n");
		return -1;
	}
	block_allocator_put_arena(&ba, arena1, ARENA_SIZE);

	// Check the lpm can allocate new pages after allocator space expansion
	do {
		write_u32(from + 8, htobe32(idx * 256));
		from[15] = 4;
		write_u32(to + 8, htobe32(idx * 256));
		to[15] = 8;
		if (lpm_wide_insert(&lpm, 16, from, to, idx))
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
	if (lpm_wide_walk(&lpm, 16, from, to, walk_func, &idx)) {
		fprintf(stdout, "walk verification failed\n");
		return -1;
	}
	if (idx != fail_idx) {
		fprintf(stdout, "invalid value count\n");
		return -1;
	}

	lpm_wide_free(&lpm);

	if (mctx.balloc_size != mctx.bfree_size) {
		fprintf(stdout,
			"alloc and free sizes should be equal %lu != %lu\n",
			mctx.balloc_size,
			mctx.bfree_size);
		return -1;
	}

	memory_context_fini(&mctx);
	free(arena1);
	free(arena0);
	return 0;
}
