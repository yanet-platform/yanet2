#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_block.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#include <endian.h>

#define ARENA_SIZE (1 << 20)
// Merge cases run three tries at once; the sanitizer build inflates
// every block allocator bucket, so the merge arena needs headroom.
#define MERGE_ARENA_SIZE (1 << 23)

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
	if (from[15] != 4) {
		return -1;
	}

	if (read_u32(to + 8) != htobe32(value * 256)) {
		return -1;
	}
	if (to[15] != 8) {
		return -1;
	}

	if (value != *(uint32_t *)check) {
		return -1;
	}

	++(*(uint32_t *)check);
	return 0;
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

static uint64_t
batch_test_rand(uint64_t *state) {
	uint64_t x = *state;
	x ^= x << 13;
	x ^= x >> 7;
	x ^= x << 17;
	*state = x;
	return x;
}

// Batched lookup must return exactly what the single-key walk returns,
// for every key and across lane-chunk boundaries (partial last chunk).
static int
test_lookup_batch_matches_scalar(void) {
	void *arena = malloc(16 << 20);
	if (arena == NULL) {
		return -1;
	}

	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 16 << 20);

	struct memory_context mctx;
	memory_context_init(&mctx, "lpm_batch", &ba);

	int rc = -1;
	uint64_t rand_state = 0x12345678;

	uint8_t prefixes[300][16];
	const int key_sizes[] = {16, 8, 4};
	size_t counts[] = {1, 31, 32, 33, 100, 1000};
	uint8_t *keys = malloc(1000 * 16);
	uint32_t *results = malloc(1000 * sizeof(uint32_t));
	if (keys == NULL || results == NULL) {
		goto out;
	}

	// Covers the sizes the dataplane actually walks: 16-byte and 4-byte
	// generic keys, and the 8-byte halves of the net6 classifiers.
	for (size_t ks = 0; ks < sizeof(key_sizes) / sizeof(key_sizes[0]);
	     ++ks) {
		const int key_size = key_sizes[ks];
		struct lpm tree;
		if (lpm_init(&tree, &mctx, "lpm")) {
			goto out;
		}

		// Prefixes at every depth, alternating full-range tail bytes
		// with single-byte tails so both page splits and slot runs are
		// covered.
		for (size_t i = 0; i < 300; i++) {
			uint8_t depth =
				1 + batch_test_rand(&rand_state) % key_size;
			memset(prefixes[i], 0x00, 16);
			for (uint8_t b = 0; b < depth; b++) {
				prefixes[i][b] =
					(uint8_t)batch_test_rand(&rand_state);
			}
			uint8_t from[16], to[16];
			memcpy(from, prefixes[i], depth);
			memset(from + depth, 0x00, key_size - depth);
			memcpy(to, prefixes[i], depth);
			memset(to + depth, 0xff, key_size - depth);
			if (lpm_insert(&tree, key_size, from, to, i)) {
				lpm_free(&tree);
				goto out;
			}
		}

		for (size_t c = 0; c < sizeof(counts) / sizeof(counts[0]);
		     c++) {
			size_t count = counts[c];
			for (size_t k = 0; k < count; k++) {
				// Alternate keys inside an inserted prefix
				// (narrow-tail randomization) with keys fully
				// outside the mapped space (default value hit).
				size_t idx = batch_test_rand(&rand_state) % 300;
				if (k % 3 != 0) {
					memcpy(keys + k * key_size,
					       prefixes[idx],
					       key_size);
					for (uint8_t b = key_size - 2;
					     b < key_size;
					     b++) {
						keys[k * key_size +
						     b] ^= (uint8_t
						)batch_test_rand(&rand_state);
					}
				} else {
					for (uint8_t b = 0; b < key_size; b++) {
						keys[k * key_size +
						     b] = (uint8_t
						)batch_test_rand(&rand_state);
					}
				}
			}

			lpm_lookup_batch(&tree, key_size, keys, results, count);
			for (size_t k = 0; k < count; k++) {
				uint32_t scalar = lpm_lookup(
					&tree, key_size, keys + k * key_size
				);
				if (scalar != results[k]) {
					fprintf(stdout,
						"key_size %d count %zu key "
						"%zu: "
						"batch %u != scalar %u\n",
						key_size,
						count,
						k,
						results[k],
						scalar);
					lpm_free(&tree);
					goto out;
				}
			}
		}

		lpm_free(&tree);
	}

	rc = 0;

out:
	free(keys);
	free(results);
	memory_context_fini(&mctx);
	free(arena);
	return rc;
}

// Verifies the merge contract over a set of keys: the merged leaf
// decoded through both remap tables equals each source lookup.
static int
merge_verify_keys(
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
			fprintf(stdout, "leaf %u out of table\n", leaf);
			return -1;
		}
		uint32_t va = lpm_lookup(a, key_size, key);
		uint32_t vb = lpm_lookup(b, key_size, key);
		if (vline_get(&table->a, leaf) != va ||
		    vline_get(&table->b, leaf) != vb) {
			fprintf(stdout,
				"mismatch at key %u: merged (%u, %u)"
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

// Fills a trie with random overlapping ranges, recording both
// endpoints of every range for boundary probes.
static void
merge_fill_random(
	struct lpm *lpm,
	uint64_t *rng,
	uint8_t key_size,
	uint32_t range_count,
	uint32_t value_top,
	uint8_t *ends
) {
	for (uint32_t idx = 0; idx < range_count; ++idx) {
		uint8_t from[key_size];
		uint8_t to[key_size];
		for (uint8_t b = 0; b < key_size; ++b) {
			from[b] = batch_test_rand(rng);
			to[b] = batch_test_rand(rng);
		}
		for (uint8_t b = 0; b < key_size; ++b) {
			if (from[b] != to[b]) {
				if (from[b] > to[b]) {
					uint8_t t = from[b];
					from[b] = to[b];
					to[b] = t;
				}
				break;
			}
		}
		lpm_insert(
			lpm,
			key_size,
			from,
			to,
			1 + batch_test_rand(rng) % value_top
		);
		memcpy(ends + idx * 2 * key_size, from, key_size);
		memcpy(ends + (idx * 2 + 1) * key_size, to, key_size);
	}
}

// Steps a big-endian key by one in either direction, reporting wrap
// around the whole key domain.
static int
key_step(uint8_t *key, uint8_t key_size, int up) {
	for (int b = key_size - 1; b >= 0; --b) {
		if (up) {
			if (key[b] != 0xff) {
				++key[b];
				return 0;
			}
			key[b] = 0x00;
		} else {
			if (key[b] != 0x00) {
				--key[b];
				return 0;
			}
			key[b] = 0xff;
		}
	}
	return -1;
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

// Creates a fixture over a fresh arena with a root context and two
// empty source tries.
static int
merge_fixture_init(struct merge_fixture *fx, size_t arena_size) {
	fx->arena = malloc(arena_size);
	if (fx->arena == NULL) {
		return -1;
	}
	block_allocator_init(&fx->allocator);
	block_allocator_put_arena(&fx->allocator, fx->arena, arena_size);
	memory_context_init(&fx->memory_context, "lpm-merge", &fx->allocator);
	memset(&fx->a, 0, sizeof(fx->a));
	memset(&fx->b, 0, sizeof(fx->b));
	memset(&fx->merged, 0, sizeof(fx->merged));
	memset(&fx->table, 0, sizeof(fx->table));
	if (lpm_init(&fx->a, &fx->memory_context, "a") ||
	    lpm_init(&fx->b, &fx->memory_context, "b")) {
		return -1;
	}
	return 0;
}

// Tears a fixture down; the tables must go before the merged trie
// they hang off.
//
// A full run allocates and frees everything through the root context,
// so the two counters must meet.
static int
merge_fixture_fini(struct merge_fixture *fx) {
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
	if (fx->memory_context.balloc_size != fx->memory_context.bfree_size) {
		fprintf(stdout,
			"leak: balloc %zu bfree %zu\n",
			fx->memory_context.balloc_size,
			fx->memory_context.bfree_size);
		free(fx->arena);
		return -1;
	}
	free(fx->arena);
	return 0;
}

// Two randomly filled tries over a small key space, checked
// exhaustively over every key.
static int
test_merge_random_small_exhaustive(void) {
	enum { KEY_SIZE = 2, KEY_COUNT = 1 << (KEY_SIZE * 8), RANGES = 40 };
	enum { ENDS_SIZE = RANGES * 2 * KEY_SIZE };

	for (uint32_t round = 0; round < 8; ++round) {
		uint64_t rng = 0x1234567 + round * 101;
		uint8_t ends[2][ENDS_SIZE];
		struct merge_fixture fx;
		if (merge_fixture_init(&fx, MERGE_ARENA_SIZE)) {
			return -1;
		}
		merge_fill_random(&fx.a, &rng, KEY_SIZE, RANGES, 50, ends[0]);
		merge_fill_random(&fx.b, &rng, KEY_SIZE, RANGES, 50, ends[1]);

		int rc = 0;
		if (lpm_merge(
			    &fx.merged,
			    &fx.memory_context,
			    "merged",
			    &fx.a,
			    &fx.b,
			    KEY_SIZE,
			    &fx.table
		    )) {
			fprintf(stdout, "merge failed at round %u\n", round);
			rc = -1;
		} else {
			uint8_t keys[KEY_COUNT * KEY_SIZE];
			for (uint32_t idx = 0; idx < KEY_COUNT; ++idx) {
				keys[idx * KEY_SIZE] = idx >> 8;
				keys[idx * KEY_SIZE + 1] = idx;
			}
			rc = merge_verify_keys(
				&fx.merged,
				&fx.a,
				&fx.b,
				&fx.table,
				KEY_SIZE,
				keys,
				KEY_COUNT
			);
		}
		if (merge_fixture_fini(&fx) || rc) {
			return -1;
		}
	}
	return 0;
}

// Wide keys: every endpoint of every source range is probed together
// with both adjacent keys, so a boundary misplaced by one key is
// caught, alongside random probes.
static int
test_merge_wide_keys_at_boundaries(void) {
	enum { KEY_SIZE = 4, RANGES = 200, RANDOM_PROBES = 20000 };
	enum { PER_TRIE_ENDS = RANGES * 2, END_COUNT = PER_TRIE_ENDS * 2 };
	enum { BOUNDARY_PROBES = END_COUNT * 3 };
	enum { PROBES = BOUNDARY_PROBES + RANDOM_PROBES };
	enum { WIDE_MERGE_ARENA_SIZE = 1 << 26 };

	uint64_t rng = 0xdeadbeef;
	uint8_t ends[END_COUNT][KEY_SIZE];
	struct merge_fixture fx;
	if (merge_fixture_init(&fx, WIDE_MERGE_ARENA_SIZE)) {
		return -1;
	}
	merge_fill_random(&fx.a, &rng, KEY_SIZE, RANGES, 1000, (uint8_t *)ends);
	merge_fill_random(
		&fx.b,
		&rng,
		KEY_SIZE,
		RANGES,
		1000,
		(uint8_t *)ends + PER_TRIE_ENDS * KEY_SIZE
	);

	int rc = 0;
	if (lpm_merge(
		    &fx.merged,
		    &fx.memory_context,
		    "merged",
		    &fx.a,
		    &fx.b,
		    KEY_SIZE,
		    &fx.table
	    )) {
		fprintf(stdout, "merge failed\n");
		rc = -1;
	} else {
		uint8_t keys[PROBES * KEY_SIZE];
		uint32_t probe_count = 0;
		for (uint32_t idx = 0; idx < END_COUNT; ++idx) {
			const uint8_t *end = ends[idx];
			memcpy(keys + probe_count * KEY_SIZE, end, KEY_SIZE);
			++probe_count;
			uint8_t step[KEY_SIZE];
			memcpy(step, end, KEY_SIZE);
			if (key_step(step, KEY_SIZE, 0) == 0) {
				memcpy(keys + probe_count * KEY_SIZE,
				       step,
				       KEY_SIZE);
				++probe_count;
			}
			memcpy(step, end, KEY_SIZE);
			if (key_step(step, KEY_SIZE, 1) == 0) {
				memcpy(keys + probe_count * KEY_SIZE,
				       step,
				       KEY_SIZE);
				++probe_count;
			}
		}
		while (probe_count < PROBES) {
			for (uint8_t b = 0; b < KEY_SIZE; ++b) {
				keys[probe_count * KEY_SIZE + b] =
					batch_test_rand(&rng);
			}
			++probe_count;
		}
		rc = merge_verify_keys(
			&fx.merged,
			&fx.a,
			&fx.b,
			&fx.table,
			KEY_SIZE,
			keys,
			PROBES
		);
	}
	if (merge_fixture_fini(&fx) || rc) {
		return -1;
	}
	return 0;
}

// Empty and one-sided sources: the merge still yields a working trie,
// and a source without a span answers with the invalid sentinel.
static int
test_merge_empty_sources(void) {
	enum { KEY_SIZE = 2 };

	for (int variant = 0; variant < 3; ++variant) {
		struct merge_fixture fx;
		if (merge_fixture_init(&fx, MERGE_ARENA_SIZE)) {
			return -1;
		}
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

		int rc = 0;
		if (lpm_merge(
			    &fx.merged,
			    &fx.memory_context,
			    "merged",
			    &fx.a,
			    &fx.b,
			    KEY_SIZE,
			    &fx.table
		    )) {
			fprintf(stdout, "merge failed at variant %d\n", variant
			);
			rc = -1;
		} else {
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
			rc = merge_verify_keys(
				&fx.merged,
				&fx.a,
				&fx.b,
				&fx.table,
				KEY_SIZE,
				keys,
				key_count
			);
		}
		if (merge_fixture_fini(&fx) || rc) {
			return -1;
		}
	}
	return 0;
}

// Identical sources: the merge collapses to the shared partition and
// both remaps agree.
static int
test_merge_identical_sources(void) {
	enum { KEY_SIZE = 2, RANGES = 30, PROBES = 512 };
	enum { ENDS_SIZE = RANGES * 2 * KEY_SIZE };

	uint64_t rng = 0xfeedface;
	uint8_t ends[2][ENDS_SIZE];
	struct merge_fixture fx;
	if (merge_fixture_init(&fx, MERGE_ARENA_SIZE)) {
		return -1;
	}
	// The trie has no clone, so the second source replays the same
	// random stream.
	merge_fill_random(&fx.a, &rng, KEY_SIZE, RANGES, 40, ends[0]);
	rng = 0xfeedface;
	merge_fill_random(&fx.b, &rng, KEY_SIZE, RANGES, 40, ends[1]);

	int rc = 0;
	if (lpm_merge(
		    &fx.merged,
		    &fx.memory_context,
		    "merged",
		    &fx.a,
		    &fx.b,
		    KEY_SIZE,
		    &fx.table
	    )) {
		fprintf(stdout, "merge failed\n");
		rc = -1;
	} else {
		uint8_t keys[PROBES * KEY_SIZE];
		for (uint32_t idx = 0; idx < PROBES; ++idx) {
			keys[idx * KEY_SIZE] = batch_test_rand(&rng);
			keys[idx * KEY_SIZE + 1] = batch_test_rand(&rng);
		}
		rc = merge_verify_keys(
			&fx.merged,
			&fx.a,
			&fx.b,
			&fx.table,
			KEY_SIZE,
			keys,
			PROBES
		);
	}
	if (merge_fixture_fini(&fx) || rc) {
		return -1;
	}
	return 0;
}

// A merge starved of memory must fail with the output trie and the
// tables returned to zero, leaving the caller's accounting balanced.
static int
test_merge_oom_returns_merged_to_zero(void) {
	enum { KEY_SIZE = 4 };

	struct merge_fixture fx;
	if (merge_fixture_init(&fx, ARENA_SIZE)) {
		return -1;
	}

	// A random four-byte key almost always opens a new page, so
	// single-key ranges hog the arena until it is exhausted.
	struct lpm hog;
	if (lpm_init(&hog, &fx.memory_context, "hog")) {
		return -1;
	}
	uint64_t rng = 0xc0ffee;
	do {
		uint8_t key[KEY_SIZE];
		for (uint8_t b = 0; b < KEY_SIZE; ++b) {
			key[b] = batch_test_rand(&rng);
		}
		if (lpm_insert(&hog, KEY_SIZE, key, key, 1)) {
			break;
		}
	} while (1);

	static const struct lpm zero_lpm;
	static const struct lpm_merge_table zero_table;
	int rc = 0;
	if (lpm_merge(
		    &fx.merged,
		    &fx.memory_context,
		    "merged",
		    &fx.a,
		    &fx.b,
		    KEY_SIZE,
		    &fx.table
	    ) == 0) {
		fprintf(stdout,
			"merge should have failed on exhausted arena\n");
		rc = -1;
	} else if (memcmp(&fx.merged, &zero_lpm, sizeof(zero_lpm)) != 0 ||
		   memcmp(&fx.table, &zero_table, sizeof(zero_table)) != 0) {
		fprintf(stdout, "failed merge left the output non-zero\n");
		rc = -1;
	}

	lpm_free(&hog);
	if (merge_fixture_fini(&fx) || rc) {
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

	if (test_lookup_batch_matches_scalar()) {
		fprintf(stdout, "test_lookup_batch_matches_scalar: FAILED\n");
		return -1;
	}

	if (test_merge_random_small_exhaustive()) {
		fprintf(stdout, "test_merge_random_small_exhaustive: FAILED\n");
		return -1;
	}

	if (test_merge_wide_keys_at_boundaries()) {
		fprintf(stdout, "test_merge_wide_keys_at_boundaries: FAILED\n");
		return -1;
	}

	if (test_merge_empty_sources()) {
		fprintf(stdout, "test_merge_empty_sources: FAILED\n");
		return -1;
	}

	if (test_merge_identical_sources()) {
		fprintf(stdout, "test_merge_identical_sources: FAILED\n");
		return -1;
	}

	if (test_merge_oom_returns_merged_to_zero()) {
		fprintf(stdout,
			"test_merge_oom_returns_merged_to_zero: FAILED\n");
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

	// Verify the pull-based iterator produces the same results.
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
