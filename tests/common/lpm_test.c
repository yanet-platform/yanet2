#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

#include "lpm_reference.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#include <endian.h>

#define ARENA_SIZE (1 << 20)

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

// Broad prefix value must survive when a narrower overlapping prefix is
// inserted afterwards.
static int
test_overlapping_prefixes(void) {
	void *arena = malloc(ARENA_SIZE);
	if (arena == NULL) {
		return -1;
	}

	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, ARENA_SIZE);

	struct memory_context mctx;
	memory_context_init(&mctx, "lpm_overlap", &ba);

	struct lpm tree;
	if (lpm_init(&tree, &mctx, "lpm")) {
		free(arena);
		return -1;
	}

	// 0.0.0.0/0 -> value 42, then 0.1.0.0/16 -> value 99.
	uint8_t from[4], to[4];
	memset(from, 0x00, 4);
	memset(to, 0xff, 4);
	lpm_insert(&tree, 4, from, to, 42);
	from[1] = 0x01;
	to[0] = 0x00;
	to[1] = 0x01;
	lpm_insert(&tree, 4, from, to, 99);

	int rc = -1;
	uint8_t k1[4] = {0x00, 0x01, 0x02, 0x03};
	uint8_t k2[4] = {0x00, 0x02, 0x00, 0x00};
	if (lpm_lookup(&tree, 4, k1) != 99) {
		fprintf(stdout, "narrow prefix lookup failed\n");
	} else if (lpm_lookup(&tree, 4, k2) != 42) {
		fprintf(stdout, "broad prefix lost after page split\n");
	} else {
		rc = 0;
	}

	lpm_free(&tree);
	memory_context_fini(&mctx);
	free(arena);
	return rc;
}

// A deterministic xorshift generator, so a failing run reproduces.
static uint64_t
next_random(uint64_t *state) {
	*state ^= *state << 13;
	*state ^= *state >> 7;
	*state ^= *state << 17;
	return *state;
}

/*
 * Packs random ranges for the reference comparison: prefixes and arbitrary
 * ranges whose ends share a random prefix, of bytes drawn from all values,
 * from the page edges or from 0..3 so that ranges overlap and meet.
 */
static void
random_ranges(uint64_t *state, uint8_t key_size, size_t count, uint8_t *out) {
	static const uint8_t edges[] = {0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff};
	uint8_t alphabet = (uint8_t)(next_random(state) % 3);
	for (size_t idx = 0; idx < count; ++idx) {
		uint8_t *from = out + idx * (2u * key_size + 2);
		uint8_t *to = from + key_size;
		for (uint8_t pos = 0; pos < 2 * key_size + 2; ++pos) {
			uint64_t r = next_random(state);
			from[pos] = alphabet == 0   ? (uint8_t)r
				    : alphabet == 1 ? edges[r % sizeof(edges)]
						    : (uint8_t)(r % 4);
		}
		uint32_t len = next_random(state) % (key_size * 8u + 1);
		bool prefix = next_random(state) % 2;
		for (uint8_t pos = 0; pos < key_size; ++pos) {
			uint32_t kept = len > pos * 8u ? len - pos * 8u : 0;
			uint8_t mask =
				kept >= 8 ? 0xff : (uint8_t)~(0xff >> kept);
			if (prefix) {
				from[pos] &= mask;
				to[pos] = from[pos] | (uint8_t)~mask;
			} else if (pos * 8u < len) {
				to[pos] = from[pos];
			}
		}
		bool inverted = filter_key_cmp(key_size, from, to) > 0;
		for (uint8_t pos = 0; inverted && pos < key_size; ++pos) {
			uint8_t swap = from[pos];
			from[pos] = to[pos];
			to[pos] = swap;
		}
	}
}

/*
 * verifies that the insert builds the tree the reference walk builds and
 * fails where the reference fails when the arena runs out.
 *
 * Seeded random ranges cover keys of 1, 2, 4, 8, 12 and 16 bytes. Over one
 * run of 16-byte ranges the budget grows from 16 KiB to 2 MiB in 16 KiB
 * steps, half a chunk of pages, so the first, the second and each later
 * chunk allocation fails in turn. A fixed run pins that a range whose start
 * is past its end maps nothing.
 */
static int
run_insert_matches_reference_test(void) {
	static const uint8_t key_sizes[] = {1, 2, 4, 8, 12, 16};
	uint8_t ranges[32 * (2 * LPM_KEY_SIZE_MAX + 2)];
	uint64_t state = 0x9e3779b97f4a7c15;
	size_t failed_pages;
	for (uint32_t run = 0; run < 2000; ++run) {
		uint8_t key_size = key_sizes[run % sizeof(key_sizes)];
		size_t count = 1 + next_random(&state) % 24;
		random_ranges(&state, key_size, count, ranges);
		if (reference_compare(
			    ranges,
			    count,
			    key_size,
			    LPM_REFERENCE_ARENA,
			    &failed_pages
		    )) {
			fprintf(stdout, "run %u: trees differ\n", run);
			return -1;
		}
	}

	random_ranges(&state, 16, 32, ranges);
	size_t failures = 0;
	size_t last_pages = 0;
	for (size_t budget = 16 << 10; budget <= (2 << 20);
	     budget += 16 << 10) {
		if (reference_compare(ranges, 32, 16, budget, &failed_pages)) {
			fprintf(stdout, "budget %zu: trees differ\n", budget);
			return -1;
		}
		failures += failed_pages != 0 && failed_pages != last_pages;
		last_pages = failed_pages ? failed_pages : last_pages;
	}
	if (failures < 8) {
		fprintf(stdout, "only %zu allocations failed\n", failures);
		return -1;
	}

	// 10.0.0.0/8, then ranges inverted at the first byte and under it.
	const uint8_t inverted[3][10] = {
		{10, 0, 0, 0, 10, 0xff, 0xff, 0xff, 0, 1},
		{20, 0, 0, 0, 10, 0, 0, 0, 0, 2},
		{10, 1, 2, 9, 10, 1, 2, 3, 0, 2},
	};
	if (reference_compare(
		    &inverted[0][0], 3, 4, LPM_REFERENCE_ARENA, &failed_pages
	    )) {
		fprintf(stdout, "an inverted range changed the tree\n");
		return -1;
	}
	return 0;
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	if (test_overlapping_prefixes()) {
		fprintf(stdout, "test_overlapping_prefixes: FAILED\n");
		return -1;
	}

	if (run_insert_matches_reference_test()) {
		fprintf(stdout, "run_insert_matches_reference_test: FAILED\n");
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

	struct lpm lpm;
	if (lpm_init(&lpm, &mctx, "lpm")) {
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
		if (lpm_insert(&lpm, 16, from, to, idx)) {
			break;
		}
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
		write_u32(from + 8, htobe32(idx * 256));
		from[15] = 4;
		write_u32(to + 8, htobe32(idx * 256));
		to[15] = 8;
		if (lpm_insert(&lpm, 16, from, to, idx)) {
			break;
		}
		++idx;
	} while (1);

	if (idx == fail_idx) {
		fprintf(stdout,
			"could not insert after allocator space expansion");
		return -1;
	}

	fail_idx = idx;

	// Verify the pull-based iterator yields every inserted range in order.
	struct lpm_iter it;
	memset(from, 0, 16);
	memset(to, 0xff, 16);
	lpm_iter_init(&it, &lpm, 16, from, to);
	uint32_t iter_idx = 0;
	while (lpm_iter_next(&it)) {
		if (read_u32(it.cur_from + 8) != htobe32(iter_idx * 256)) {
			fprintf(stdout, "iter from mismatch at %u\n", iter_idx);
			return -1;
		}
		if (it.cur_from[15] != 4) {
			fprintf(stdout,
				"iter from[15] mismatch at %u\n",
				iter_idx);
			return -1;
		}
		if (read_u32(it.cur_to + 8) != htobe32(iter_idx * 256)) {
			fprintf(stdout, "iter to mismatch at %u\n", iter_idx);
			return -1;
		}
		if (it.cur_to[15] != 8) {
			fprintf(stdout, "iter to[15] mismatch at %u\n", iter_idx
			);
			return -1;
		}
		if (it.cur_value != iter_idx) {
			fprintf(stdout,
				"iter value mismatch at %u: got %u\n",
				iter_idx,
				it.cur_value);
			return -1;
		}
		++iter_idx;
	}
	if (iter_idx != fail_idx) {
		fprintf(stdout,
			"iter count mismatch: got %u, expected %u\n",
			iter_idx,
			fail_idx);
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

	memory_context_fini(&mctx);
	free(arena1);
	free(arena0);
	return 0;
}
