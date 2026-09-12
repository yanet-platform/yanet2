#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ARENA_SIZE (1 << 24)

static uint64_t rng_state = 0x243f6a8885a308d3ULL;
static uint64_t
rnd(void) {
	rng_state ^= rng_state << 13;
	rng_state ^= rng_state >> 7;
	rng_state ^= rng_state << 17;
	return rng_state;
}

// Verifies the merge contract over a set of keys: the merged leaf
// decoded through both remap tables equals each source lookup.
static int
verify_keys(
	const struct lpm *merged,
	const struct lpm *a,
	const struct lpm *b,
	struct lpm_merge_table *table,
	uint8_t key_size,
	const uint8_t *keys,
	uint32_t key_count
) {
	for (uint32_t idx = 0; idx < key_count; ++idx) {
		const uint8_t *key = keys + idx * key_size;
		uint32_t leaf = lpm_lookup(merged, key_size, key);
		if (leaf >= table->a.size || leaf >= table->b.size) {
			printf("leaf %u out of table\n", leaf);
			return -1;
		}
		uint32_t va = lpm_lookup(a, key_size, key);
		uint32_t vb = lpm_lookup(b, key_size, key);
		if (vline_get(&table->a, leaf) != va ||
		    vline_get(&table->b, leaf) != vb) {
			printf("mismatch at key %u: merged (%u, %u)"
			       " vs sources (%u, %u)\n",
			       idx,
			       vline_get(&table->a, leaf),
			       vline_get(&table->b, leaf),
			       va,
			       vb);
			return -1;
		}
	}
	return 0;
}

// Fills a trie with random overlapping ranges.
static void
fill_random(
	struct lpm *lpm,
	uint8_t key_size,
	uint32_t range_count,
	uint32_t value_top
) {
	for (uint32_t idx = 0; idx < range_count; ++idx) {
		uint8_t from[key_size];
		uint8_t to[key_size];
		for (uint32_t b = 0; b < key_size; ++b) {
			from[b] = rnd();
			to[b] = rnd();
		}
		for (uint32_t b = 0; b < key_size; ++b) {
			if (from[b] != to[b]) {
				if (from[b] > to[b]) {
					uint8_t t = from[b];
					from[b] = to[b];
					to[b] = t;
				}
				break;
			}
		}
		lpm_insert(lpm, key_size, from, to, 1 + rnd() % value_top);
	}
}

struct merge_fixture {
	void *arena;
	struct block_allocator allocator;
	struct memory_context memory_context;
	struct lpm a;
	struct lpm b;
	struct lpm merged;
	struct lpm_merge_table table;
};

static void
fixture_init(struct merge_fixture *fx) {
	fx->arena = malloc(ARENA_SIZE);
	block_allocator_init(&fx->allocator);
	block_allocator_put_arena(&fx->allocator, fx->arena, ARENA_SIZE);
	memory_context_init(
		&fx->memory_context, "lpm-merge-test", &fx->allocator
	);
	memset(&fx->a, 0, sizeof(fx->a));
	memset(&fx->b, 0, sizeof(fx->b));
	memset(&fx->merged, 0, sizeof(fx->merged));
	memset(&fx->table, 0, sizeof(fx->table));
}

static void
fixture_fini(struct merge_fixture *fx) {
	// The tables must go before the merged trie they hang off.
	lpm_merge_table_free(&fx->table);
	if (fx->merged.page_count != 0) {
		lpm_free(&fx->merged);
	}
	if (fx->a.page_count != 0) {
		lpm_free(&fx->a);
	}
	if (fx->b.page_count != 0) {
		lpm_free(&fx->b);
	}
	// A full run allocates and frees everything through the root
	// context, so the two counters must meet.
	if (fx->memory_context.balloc_size != fx->memory_context.bfree_size) {
		printf("leak: balloc %zu bfree %zu\n",
		       fx->memory_context.balloc_size,
		       fx->memory_context.bfree_size);
		exit(1);
	}
	free(fx->arena);
}

// Two randomly filled tries over a small key space, checked
// exhaustively over every key.
static int
test_exhaustive_small(void) {
	enum { KEY_SIZE = 2, KEY_COUNT = 1 << (KEY_SIZE * 8), RANGES = 40 };

	for (uint32_t round = 0; round < 8; ++round) {
		rng_state = 0x1234567 + round * 101;
		struct merge_fixture fx;
		fixture_init(&fx);
		lpm_init(&fx.a, &fx.memory_context, "a");
		lpm_init(&fx.b, &fx.memory_context, "b");
		fill_random(&fx.a, KEY_SIZE, RANGES, 50);
		fill_random(&fx.b, KEY_SIZE, RANGES, 50);

		if (lpm_merge(
			    &fx.merged,
			    &fx.memory_context,
			    "merged",
			    &fx.a,
			    &fx.b,
			    KEY_SIZE,
			    &fx.table
		    )) {
			printf("merge failed\n");
			fixture_fini(&fx);
			return -1;
		}

		uint8_t keys[KEY_COUNT * KEY_SIZE];
		for (uint32_t idx = 0; idx < KEY_COUNT; ++idx) {
			keys[idx * KEY_SIZE] = idx >> 8;
			keys[idx * KEY_SIZE + 1] = idx;
		}
		int rc = verify_keys(
			&fx.merged,
			&fx.a,
			&fx.b,
			&fx.table,
			KEY_SIZE,
			keys,
			KEY_COUNT
		);
		fixture_fini(&fx);
		if (rc) {
			return rc;
		}
	}
	return 0;
}

// Wide keys: every distinct source range boundary pair must be
// distinguished, checked over random keys and over the boundaries
// themselves.
static int
test_wide_keys(void) {
	enum { KEY_SIZE = 4, RANGES = 200, PROBES = 20000 };

	rng_state = 0xdeadbeef;
	struct merge_fixture fx;
	fixture_init(&fx);
	lpm_init(&fx.a, &fx.memory_context, "a");
	lpm_init(&fx.b, &fx.memory_context, "b");
	fill_random(&fx.a, KEY_SIZE, RANGES, 1000);
	fill_random(&fx.b, KEY_SIZE, RANGES, 1000);

	if (lpm_merge(
		    &fx.merged,
		    &fx.memory_context,
		    "merged",
		    &fx.a,
		    &fx.b,
		    KEY_SIZE,
		    &fx.table
	    )) {
		printf("merge failed\n");
		fixture_fini(&fx);
		return -1;
	}

	uint8_t keys[PROBES * KEY_SIZE];
	for (uint32_t idx = 0; idx < PROBES; ++idx) {
		for (uint32_t b = 0; b < KEY_SIZE; ++b) {
			keys[idx * KEY_SIZE + b] = rnd();
		}
	}
	int rc = verify_keys(
		&fx.merged, &fx.a, &fx.b, &fx.table, KEY_SIZE, keys, PROBES
	);
	fixture_fini(&fx);
	if (rc) {
		return rc;
	}
	return 0;
}

// Empty sources: every lookup falls through to the invalid sentinel,
// and the merge still yields a working trie with one leaf class.
static int
test_empty_sources(void) {
	enum { KEY_SIZE = 2 };

	for (int variant = 0; variant < 3; ++variant) {
		struct merge_fixture fx;
		fixture_init(&fx);
		lpm_init(&fx.a, &fx.memory_context, "a");
		lpm_init(&fx.b, &fx.memory_context, "b");
		if (variant == 1 || variant == 2) {
			uint8_t from[KEY_SIZE] = {10, 0};
			uint8_t to[KEY_SIZE] = {10, 255};
			lpm_insert(&fx.a, KEY_SIZE, from, to, 7);
		}
		if (variant == 2) {
			uint8_t from[KEY_SIZE] = {20, 4};
			uint8_t to[KEY_SIZE] = {20, 16};
			lpm_insert(&fx.b, KEY_SIZE, from, to, 9);
		}

		if (lpm_merge(
			    &fx.merged,
			    &fx.memory_context,
			    "merged",
			    &fx.a,
			    &fx.b,
			    KEY_SIZE,
			    &fx.table
		    )) {
			printf("merge failed\n");
			fixture_fini(&fx);
			return -1;
		}

		uint8_t keys[8 * KEY_SIZE];
		uint32_t key_count = 0;
		uint8_t probe[2];
		for (probe[0] = 0; probe[0] < 4; ++probe[0]) {
			for (probe[1] = 0; probe[1] < 2; ++probe[1]) {
				memcpy(keys + key_count * KEY_SIZE,
				       probe,
				       KEY_SIZE);
				++key_count;
			}
		}
		int rc = verify_keys(
			&fx.merged,
			&fx.a,
			&fx.b,
			&fx.table,
			KEY_SIZE,
			keys,
			key_count
		);
		fixture_fini(&fx);
		if (rc) {
			return rc;
		}
	}
	return 0;
}

// Identical sources: the merge collapses to the shared partition with
// both remaps agreeing.
static int
test_identical_sources(void) {
	enum { KEY_SIZE = 2 };

	rng_state = 0xfeedface;
	struct merge_fixture fx;
	fixture_init(&fx);
	lpm_init(&fx.a, &fx.memory_context, "a");
	lpm_init(&fx.b, &fx.memory_context, "b");
	// lpm offers no clone, so B replays the same random stream.
	rng_state = 0xfeedface;
	fill_random(&fx.a, KEY_SIZE, 30, 40);
	rng_state = 0xfeedface;
	fill_random(&fx.b, KEY_SIZE, 30, 40);

	if (lpm_merge(
		    &fx.merged,
		    &fx.memory_context,
		    "merged",
		    &fx.a,
		    &fx.b,
		    KEY_SIZE,
		    &fx.table
	    )) {
		printf("merge failed\n");
		fixture_fini(&fx);
		return -1;
	}

	uint8_t keys[512 * KEY_SIZE];
	for (uint32_t idx = 0; idx < 512; ++idx) {
		keys[idx * KEY_SIZE] = rnd();
		keys[idx * KEY_SIZE + 1] = rnd();
	}
	int rc = verify_keys(
		&fx.merged, &fx.a, &fx.b, &fx.table, KEY_SIZE, keys, 512
	);
	fixture_fini(&fx);
	return rc;
}

int
main(void) {
	if (test_exhaustive_small()) {
		return 1;
	}
	if (test_wide_keys()) {
		return 1;
	}
	if (test_empty_sources()) {
		return 1;
	}
	if (test_identical_sources()) {
		return 1;
	}
	printf("lpm merge ok\n");
	return 0;
}
