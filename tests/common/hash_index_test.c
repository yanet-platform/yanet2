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

static int
match_item(uint32_t value, const void *data) {
	const struct eq_context *ctx = (const struct eq_context *)data;
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
	if (test_fini_idempotent() != 0) {
		LOG(ERROR, "test_fini_idempotent failed");
		return -1;
	}

	LOG(INFO, "hash_index tests: OK");
	return 0;
}
