#include "common/hash.h"
#include "common/hash_index.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#define ARENA_SIZE ((size_t)(1u << 22))

struct test_item {
	uint32_t key;
	uint32_t tag;
};

#define ITEM_COUNT 256
static struct test_item items[ITEM_COUNT];

struct eq_context {
	uint32_t want_key;
	uint32_t want_tag;
};

// Equality callbacks received so far; reset and asserted by the test
// proving that foreign slots bypass the callback.
static uint32_t match_item_calls;

static int
match_item(uint32_t value, const void *data) {
	const struct eq_context *ctx = (const struct eq_context *)data;
	match_item_calls += 1;
	if (items[value].key == ctx->want_key &&
	    items[value].tag == ctx->want_tag) {
		return 0;
	}
	return 1;
}

static uint32_t
hash_key(uint32_t key) {
	return (uint32_t)wyhash64(key);
}

struct fixture {
	void *arena;
	struct block_allocator allocator;
	struct memory_context memory_context;
	struct hash_index index;
};

static int
fixture_init(struct fixture *fix, uint32_t capacity) {
	fix->arena = malloc(ARENA_SIZE);
	TEST_ASSERT(fix->arena != NULL, "arena allocation failed");

	block_allocator_init(&fix->allocator);
	block_allocator_put_arena(&fix->allocator, fix->arena, ARENA_SIZE);

	TEST_ASSERT(
		memory_context_init(
			&fix->memory_context, "test", &fix->allocator
		) == 0,
		"memory_context_init failed"
	);

	TEST_ASSERT(
		hash_index_init(&fix->index, &fix->memory_context, capacity) ==
			0,
		"hash_index_init failed"
	);

	return 0;
}

static void
fixture_fini(struct fixture *fix) {
	hash_index_fini(&fix->index);
	free(fix->arena);
}

// Look up the item carrying the given key and tag, reporting absence
// when none matches.
static uint32_t
lookup_item(const struct fixture *fix, uint32_t key, uint32_t tag) {
	struct eq_context ctx = {.want_key = key, .want_tag = tag};
	return hash_index_lookup(&fix->index, hash_key(key), match_item, &ctx);
}

// Inserts one item and looks it up by hash plus tag, then confirms a
// mismatched tag yields HASH_INDEX_INVALID.
static int
test_single_insert_lookup(void) {
	struct fixture fix;
	if (fixture_init(&fix, ITEM_COUNT) != 0) {
		return TEST_FAILED;
	}

	items[0] = (struct test_item){.key = 42, .tag = 100};
	TEST_ASSERT(
		hash_index_insert(&fix.index, hash_key(42), 0) == 0,
		"insert failed"
	);
	TEST_ASSERT(fix.index.count == 1, "count must be 1 after insert");

	struct eq_context ctx = {.want_key = 42, .want_tag = 100};
	uint32_t result =
		hash_index_lookup(&fix.index, hash_key(42), match_item, &ctx);
	TEST_ASSERT_EQUAL(0, result, "single insert lookup");

	ctx.want_tag = 999;
	result = hash_index_lookup(&fix.index, hash_key(42), match_item, &ctx);
	TEST_ASSERT_EQUAL(HASH_INDEX_INVALID, result, "wrong tag must miss");

	fixture_fini(&fix);
	return 0;
}

// Verifies a lookup on a freshly initialised (empty) index returns
// HASH_INDEX_INVALID without crashing.
static int
test_lookup_empty(void) {
	struct fixture fix;
	if (fixture_init(&fix, ITEM_COUNT) != 0) {
		return TEST_FAILED;
	}

	struct eq_context ctx = {.want_key = 1, .want_tag = 1};
	uint32_t result =
		hash_index_lookup(&fix.index, hash_key(1), match_item, &ctx);
	TEST_ASSERT_EQUAL(HASH_INDEX_INVALID, result, "empty index must miss");

	fixture_fini(&fix);
	return 0;
}

// Exercises the zero-capacity index.
//
// hash_index_init(0) must yield an index whose lookups always miss and
// whose inserts are rejected without dereferencing the NULL entries slot.
static int
test_zero_capacity(void) {
	struct fixture fix;
	if (fixture_init(&fix, 0) != 0) {
		return TEST_FAILED;
	}

	TEST_ASSERT(fix.index.capacity == 0, "capacity must be 0");
	TEST_ASSERT(fix.index.count == 0, "count must be 0");

	struct eq_context ctx = {.want_key = 1, .want_tag = 1};
	uint32_t result =
		hash_index_lookup(&fix.index, hash_key(1), match_item, &ctx);
	TEST_ASSERT_EQUAL(
		HASH_INDEX_INVALID, result, "zero-cap lookup must miss"
	);

	TEST_ASSERT(
		hash_index_insert(&fix.index, hash_key(1), 0) == -1,
		"zero-cap insert must be rejected"
	);
	TEST_ASSERT(
		fix.index.count == 0, "count must stay 0 after rejected insert"
	);

	fixture_fini(&fix);
	return 0;
}

// Inserts several items with distinct keys and looks each one up.
static int
test_distinct_keys(void) {
	struct fixture fix;
	if (fixture_init(&fix, ITEM_COUNT) != 0) {
		return TEST_FAILED;
	}

	for (uint32_t idx = 0; idx < 16; ++idx) {
		items[idx] = (struct test_item){.key = idx * 7 + 3,
						.tag = idx * 13 + 5};
		TEST_ASSERT(
			hash_index_insert(
				&fix.index, hash_key(items[idx].key), idx
			) == 0,
			"insert failed at %u",
			idx
		);
	}
	TEST_ASSERT(fix.index.count == 16, "count must be 16");

	for (uint32_t idx = 0; idx < 16; ++idx) {
		struct eq_context ctx = {
			.want_key = items[idx].key, .want_tag = items[idx].tag
		};
		uint32_t result = hash_index_lookup(
			&fix.index, hash_key(items[idx].key), match_item, &ctx
		);
		TEST_ASSERT_EQUAL(idx, result, "distinct key lookup %u", idx);
	}

	fixture_fini(&fix);
	return 0;
}

// Fills the index to its declared capacity.
//
// Verifies every one of the capacity inserts succeeds, the next insert is
// rejected once count reaches capacity, and that every stored value is
// still reachable.
static int
test_fill_capacity(void) {
	struct fixture fix;
	if (fixture_init(&fix, ITEM_COUNT) != 0) {
		return TEST_FAILED;
	}

	for (uint32_t idx = 0; idx < ITEM_COUNT; ++idx) {
		items[idx] = (struct test_item){.key = idx * 7 + 1,
						.tag = idx * 31 + 2};
		TEST_ASSERT(
			hash_index_insert(
				&fix.index, hash_key(items[idx].key), idx
			) == 0,
			"insert failed at %u",
			idx
		);
	}
	TEST_ASSERT(fix.index.count == ITEM_COUNT, "count must reach capacity");
	TEST_ASSERT(
		hash_index_insert(&fix.index, hash_key(0), 0) == -1,
		"insert past capacity must fail"
	);

	for (uint32_t idx = 0; idx < ITEM_COUNT; ++idx) {
		struct eq_context ctx = {
			.want_key = items[idx].key, .want_tag = items[idx].tag
		};
		uint32_t result = hash_index_lookup(
			&fix.index, hash_key(items[idx].key), match_item, &ctx
		);
		TEST_ASSERT_EQUAL(idx, result, "full-table lookup %u", idx);
	}

	struct eq_context miss = {.want_key = 0, .want_tag = 0};
	uint32_t result =
		hash_index_lookup(&fix.index, hash_key(0), match_item, &miss);
	TEST_ASSERT_EQUAL(HASH_INDEX_INVALID, result, "absent key must miss");

	fixture_fini(&fix);
	return 0;
}

// Inserts multiple values sharing the same hash (differing only by tag)
// and confirms the equality callback disambiguates them.
static int
test_same_hash_multiple_values(void) {
	struct fixture fix;
	if (fixture_init(&fix, ITEM_COUNT) != 0) {
		return TEST_FAILED;
	}

	items[0] = (struct test_item){.key = 7, .tag = 10};
	items[1] = (struct test_item){.key = 7, .tag = 20};
	items[2] = (struct test_item){.key = 7, .tag = 30};

	uint32_t hash = hash_key(7);
	for (uint32_t idx = 0; idx < 3; ++idx) {
		TEST_ASSERT(
			hash_index_insert(&fix.index, hash, idx) == 0,
			"insert failed at %u",
			idx
		);
	}
	TEST_ASSERT(fix.index.count == 3, "count must be 3");

	for (uint32_t idx = 0; idx < 3; ++idx) {
		struct eq_context ctx = {
			.want_key = 7, .want_tag = items[idx].tag
		};
		uint32_t result =
			hash_index_lookup(&fix.index, hash, match_item, &ctx);
		TEST_ASSERT_EQUAL(idx, result, "same-hash lookup %u", idx);
	}

	struct eq_context miss = {.want_key = 7, .want_tag = 999};
	uint32_t result =
		hash_index_lookup(&fix.index, hash, match_item, &miss);
	TEST_ASSERT_EQUAL(HASH_INDEX_INVALID, result, "absent tag must miss");

	fixture_fini(&fix);
	return 0;
}

// Inserts two entries whose hashes differ yet share a bucket, then
// confirms each lookup still reaches its own entry and consults the
// equality callback for that entry alone: the foreign slot is bypassed
// without a callback round.
static int
test_bucket_collision_distinct_hashes(void) {
	struct fixture fix;
	if (fixture_init(&fix, ITEM_COUNT) != 0) {
		return TEST_FAILED;
	}

	uint32_t slots = ITEM_COUNT * HASH_INDEX_SPARSE_FACTOR;
	uint32_t far_hash = 1 + slots;

	items[0] = (struct test_item){.key = 10, .tag = 1};
	items[1] = (struct test_item){.key = 20, .tag = 2};

	TEST_ASSERT(
		hash_index_insert(&fix.index, far_hash, 0) == 0,
		"far insert failed"
	);
	TEST_ASSERT(
		hash_index_insert(&fix.index, 1, 1) == 0, "near insert failed"
	);
	TEST_ASSERT(fix.index.count == 2, "count must be 2");

	struct eq_context far_ctx = {.want_key = 10, .want_tag = 1};
	match_item_calls = 0;
	uint32_t result =
		hash_index_lookup(&fix.index, far_hash, match_item, &far_ctx);
	TEST_ASSERT_EQUAL(0, result, "far-hash entry must be found");
	TEST_ASSERT_EQUAL(
		1, match_item_calls, "far-hash lookup consults one slot"
	);

	struct eq_context near_ctx = {.want_key = 20, .want_tag = 2};
	match_item_calls = 0;
	result = hash_index_lookup(&fix.index, 1, match_item, &near_ctx);
	TEST_ASSERT_EQUAL(1, result, "entry behind a foreign slot");
	TEST_ASSERT_EQUAL(
		1, match_item_calls, "foreign slot must bypass the callback"
	);

	struct eq_context miss = {.want_key = 30, .want_tag = 3};
	match_item_calls = 0;
	result = hash_index_lookup(&fix.index, 1, match_item, &miss);
	TEST_ASSERT_EQUAL(
		HASH_INDEX_INVALID, result, "absent key behind a foreign slot"
	);
	TEST_ASSERT_EQUAL(
		1, match_item_calls, "absent key consults its own slot only"
	);

	fixture_fini(&fix);
	return 0;
}

// Confirms that hash_index_fini is idempotent and safe to call twice.
static int
test_fini_idempotent(void) {
	struct fixture fix;
	if (fixture_init(&fix, ITEM_COUNT) != 0) {
		return TEST_FAILED;
	}

	items[0] = (struct test_item){.key = 1, .tag = 1};
	TEST_ASSERT(
		hash_index_insert(&fix.index, hash_key(1), 0) == 0,
		"insert failed"
	);

	hash_index_fini(&fix.index);
	hash_index_fini(&fix.index);

	TEST_ASSERT(fix.index.count == 0, "count must be 0 after fini");
	TEST_ASSERT(fix.index.capacity == 0, "capacity must be 0 after fini");

	free(fix.arena);
	return 0;
}

static int
test_rehash_grow_preserves_entries(void) {
	struct fixture fix;
	if (fixture_init(&fix, 8) != 0) {
		return TEST_FAILED;
	}

	for (uint32_t idx = 0; idx < 8; ++idx) {
		items[idx] = (struct test_item){.key = idx * 7 + 3,
						.tag = idx * 13 + 5};
		TEST_ASSERT(
			hash_index_insert(
				&fix.index, hash_key(items[idx].key), idx
			) == 0,
			"insert failed at %u",
			idx
		);
	}

	TEST_ASSERT(hash_index_rehash(&fix.index, 32) == 0, "rehash failed");
	TEST_ASSERT(fix.index.capacity == 32, "capacity must follow rehash");
	TEST_ASSERT(fix.index.count == 8, "count must survive rehash");

	for (uint32_t idx = 0; idx < 8; ++idx) {
		uint32_t result =
			lookup_item(&fix, items[idx].key, items[idx].tag);
		TEST_ASSERT_EQUAL(idx, result, "post-grow lookup %u", idx);
	}

	fixture_fini(&fix);
	return 0;
}

static int
test_rehash_shrink_to_count(void) {
	struct fixture fix;
	if (fixture_init(&fix, 32) != 0) {
		return TEST_FAILED;
	}

	for (uint32_t idx = 0; idx < 5; ++idx) {
		items[idx] = (struct test_item){.key = idx * 11 + 2,
						.tag = idx * 17 + 9};
		TEST_ASSERT(
			hash_index_insert(
				&fix.index, hash_key(items[idx].key), idx
			) == 0,
			"insert failed at %u",
			idx
		);
	}

	TEST_ASSERT(hash_index_rehash(&fix.index, 5) == 0, "rehash failed");
	TEST_ASSERT(fix.index.capacity == 5, "capacity must follow rehash");
	TEST_ASSERT(fix.index.count == 5, "count must survive rehash");

	for (uint32_t idx = 0; idx < 5; ++idx) {
		uint32_t result =
			lookup_item(&fix, items[idx].key, items[idx].tag);
		TEST_ASSERT_EQUAL(idx, result, "post-shrink lookup %u", idx);
	}

	fixture_fini(&fix);
	return 0;
}

static int
test_rehash_below_count_rejected(void) {
	struct fixture fix;
	if (fixture_init(&fix, 8) != 0) {
		return TEST_FAILED;
	}

	for (uint32_t idx = 0; idx < 3; ++idx) {
		items[idx] = (struct test_item){.key = idx * 5 + 1,
						.tag = idx * 7 + 4};
		TEST_ASSERT(
			hash_index_insert(
				&fix.index, hash_key(items[idx].key), idx
			) == 0,
			"insert failed at %u",
			idx
		);
	}

	TEST_ASSERT(
		hash_index_rehash(&fix.index, 2) == -1,
		"undersized rehash must be refused"
	);
	TEST_ASSERT(fix.index.capacity == 8, "capacity must be unchanged");
	TEST_ASSERT(fix.index.count == 3, "count must be unchanged");

	for (uint32_t idx = 0; idx < 3; ++idx) {
		uint32_t result =
			lookup_item(&fix, items[idx].key, items[idx].tag);
		TEST_ASSERT_EQUAL(idx, result, "refused-rehash lookup %u", idx);
	}

	fixture_fini(&fix);
	return 0;
}

static int
test_rehash_from_zero_capacity(void) {
	struct fixture fix;
	if (fixture_init(&fix, 0) != 0) {
		return TEST_FAILED;
	}

	TEST_ASSERT(fix.index.capacity == 0, "capacity must start at 0");
	TEST_ASSERT(
		hash_index_rehash(&fix.index, 0) == -1,
		"zero rehash must be refused even from zero capacity"
	);
	TEST_ASSERT(hash_index_rehash(&fix.index, 4) == 0, "rehash failed");
	TEST_ASSERT(fix.index.capacity == 4, "capacity must follow rehash");

	items[0] = (struct test_item){.key = 42, .tag = 100};
	TEST_ASSERT(
		hash_index_insert(&fix.index, hash_key(42), 0) == 0,
		"insert failed"
	);
	TEST_ASSERT_EQUAL(0, lookup_item(&fix, 42, 100), "late-init lookup");

	fixture_fini(&fix);
	return 0;
}

static int
test_rehash_same_capacity_noop(void) {
	struct fixture fix;
	if (fixture_init(&fix, 8) != 0) {
		return TEST_FAILED;
	}

	for (uint32_t idx = 0; idx < 3; ++idx) {
		items[idx] = (struct test_item){.key = idx * 9 + 6,
						.tag = idx * 15 + 3};
		TEST_ASSERT(
			hash_index_insert(
				&fix.index, hash_key(items[idx].key), idx
			) == 0,
			"insert failed at %u",
			idx
		);
	}

	TEST_ASSERT(
		hash_index_rehash(&fix.index, 8) == 0,
		"same rehash must succeed"
	);
	TEST_ASSERT(fix.index.capacity == 8, "capacity must be unchanged");
	TEST_ASSERT(fix.index.count == 3, "count must be unchanged");

	for (uint32_t idx = 0; idx < 3; ++idx) {
		uint32_t result =
			lookup_item(&fix, items[idx].key, items[idx].tag);
		TEST_ASSERT_EQUAL(idx, result, "no-op rehash lookup %u", idx);
	}

	fixture_fini(&fix);
	return 0;
}

// The failure is forced by requesting a buffer larger than the fixture
// arena can hold.
static int
test_rehash_failure_leaves_index_usable(void) {
	struct fixture fix;
	if (fixture_init(&fix, 8) != 0) {
		return TEST_FAILED;
	}

	for (uint32_t idx = 0; idx < 3; ++idx) {
		items[idx] = (struct test_item){.key = idx * 3 + 8,
						.tag = idx * 21 + 7};
		TEST_ASSERT(
			hash_index_insert(
				&fix.index, hash_key(items[idx].key), idx
			) == 0,
			"insert failed at %u",
			idx
		);
	}

	TEST_ASSERT(
		hash_index_rehash(&fix.index, 1u << 20) == -1,
		"oversized rehash must fail"
	);
	TEST_ASSERT(fix.index.capacity == 8, "capacity must be unchanged");
	TEST_ASSERT(fix.index.count == 3, "count must be unchanged");

	for (uint32_t idx = 0; idx < 3; ++idx) {
		uint32_t result =
			lookup_item(&fix, items[idx].key, items[idx].tag);
		TEST_ASSERT_EQUAL(idx, result, "post-failure lookup %u", idx);
	}

	items[3] = (struct test_item){.key = 77, .tag = 88};
	TEST_ASSERT(
		hash_index_insert(&fix.index, hash_key(77), 3) == 0,
		"insert after failed rehash"
	);
	TEST_ASSERT_EQUAL(3, lookup_item(&fix, 77, 88), "post-failure insert");

	fixture_fini(&fix);
	return 0;
}

// The capacity sits one past the slot-count bound, so scaling it by the
// sparse factor would wrap to a near-zero slot count.
static int
test_rehash_overflowing_capacity_rejected(void) {
	struct fixture fix;
	if (fixture_init(&fix, 8) != 0) {
		return TEST_FAILED;
	}

	struct hash_index probe;
	TEST_ASSERT(
		hash_index_init(&probe, &fix.memory_context, 0x40000001u) == -1,
		"overflowing init capacity must be refused"
	);
	hash_index_fini(&probe);

	items[0] = (struct test_item){.key = 4, .tag = 5};
	TEST_ASSERT(
		hash_index_insert(&fix.index, hash_key(4), 0) == 0,
		"insert failed"
	);
	TEST_ASSERT(
		hash_index_rehash(&fix.index, 0x40000001u) == -1,
		"overflowing rehash capacity must be refused"
	);
	TEST_ASSERT(fix.index.capacity == 8, "capacity must be unchanged");
	TEST_ASSERT_EQUAL(0, lookup_item(&fix, 4, 5), "index must stay usable");

	fixture_fini(&fix);
	return 0;
}

int
main(void) {
	log_enable_name("info");

	if (test_single_insert_lookup() != 0) {
		LOG(ERROR, "test_single_insert_lookup failed");
		return -1;
	}
	if (test_lookup_empty() != 0) {
		LOG(ERROR, "test_lookup_empty failed");
		return -1;
	}
	if (test_zero_capacity() != 0) {
		LOG(ERROR, "test_zero_capacity failed");
		return -1;
	}
	if (test_distinct_keys() != 0) {
		LOG(ERROR, "test_distinct_keys failed");
		return -1;
	}
	if (test_fill_capacity() != 0) {
		LOG(ERROR, "test_fill_capacity failed");
		return -1;
	}
	if (test_same_hash_multiple_values() != 0) {
		LOG(ERROR, "test_same_hash_multiple_values failed");
		return -1;
	}
	if (test_bucket_collision_distinct_hashes() != 0) {
		LOG(ERROR, "test_bucket_collision_distinct_hashes failed");
		return -1;
	}
	if (test_fini_idempotent() != 0) {
		LOG(ERROR, "test_fini_idempotent failed");
		return -1;
	}
	if (test_rehash_grow_preserves_entries() != 0) {
		LOG(ERROR, "test_rehash_grow_preserves_entries failed");
		return -1;
	}
	if (test_rehash_shrink_to_count() != 0) {
		LOG(ERROR, "test_rehash_shrink_to_count failed");
		return -1;
	}
	if (test_rehash_below_count_rejected() != 0) {
		LOG(ERROR, "test_rehash_below_count_rejected failed");
		return -1;
	}
	if (test_rehash_from_zero_capacity() != 0) {
		LOG(ERROR, "test_rehash_from_zero_capacity failed");
		return -1;
	}
	if (test_rehash_same_capacity_noop() != 0) {
		LOG(ERROR, "test_rehash_same_capacity_noop failed");
		return -1;
	}
	if (test_rehash_failure_leaves_index_usable() != 0) {
		LOG(ERROR, "test_rehash_failure_leaves_index_usable failed");
		return -1;
	}
	if (test_rehash_overflowing_capacity_rejected() != 0) {
		LOG(ERROR, "test_rehash_overflowing_capacity_rejected failed");
		return -1;
	}

	LOG(INFO, "hash_index tests: OK");
	return 0;
}
