#include "common/memory.h"
#include "common/memory_block.h"
#include "common/str_index.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define ARENA_SIZE ((size_t)(1u << 22))
#define KEY_SIZE 32
#define NAME_COUNT 1024

static char names[NAME_COUNT][KEY_SIZE];

static const char *
read_name(uint32_t index, const void *data) {
	(void)data;
	return names[index];
}

static void
set_name(uint32_t index, const char *text) {
	memset(names[index], 0, KEY_SIZE);
	strncpy(names[index], text, KEY_SIZE - 1);
}

struct fixture {
	void *arena;
	struct block_allocator allocator;
	struct memory_context memory_context;
	struct str_index index;
};

static int
fixture_init(struct fixture *fix) {
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
		str_index_init(&fix->index, &fix->memory_context) == 0,
		"str_index_init failed"
	);

	return 0;
}

static void
fixture_fini(struct fixture *fix) {
	str_index_fini(&fix->index);
	free(fix->arena);
}

// Inserts one key and looks it up, then confirms a different key misses.
static int
test_single_insert_lookup(void) {
	struct fixture fix;
	if (fixture_init(&fix) != 0)
		return TEST_FAILED;

	set_name(0, "alpha");
	TEST_ASSERT(
		str_index_insert(
			&fix.index, names[0], KEY_SIZE, 0, read_name, NULL
		) == 0,
		"insert failed"
	);

	uint32_t result = str_index_lookup(
		&fix.index, names[0], KEY_SIZE, read_name, NULL
	);
	TEST_ASSERT_EQUAL(0, result, "single insert lookup");

	result = str_index_lookup(
		&fix.index, "absent", KEY_SIZE, read_name, NULL
	);
	TEST_ASSERT_EQUAL(STR_INDEX_INVALID, result, "absent key must miss");

	fixture_fini(&fix);
	return 0;
}

// Verifies a lookup on a freshly initialised (empty) index returns
// STR_INDEX_INVALID without crashing or allocating.
static int
test_lookup_empty(void) {
	struct fixture fix;
	if (fixture_init(&fix) != 0)
		return TEST_FAILED;

	TEST_ASSERT(
		fix.index.hash_index.capacity == 0, "capacity must start at 0"
	);
	TEST_ASSERT(fix.index.hash_index.count == 0, "count must start at 0");

	uint32_t result =
		str_index_lookup(&fix.index, "x", KEY_SIZE, read_name, NULL);
	TEST_ASSERT_EQUAL(STR_INDEX_INVALID, result, "empty index must miss");

	fixture_fini(&fix);
	return 0;
}

// Inserts several distinct keys and looks each one up.
static int
test_distinct_keys(void) {
	struct fixture fix;
	if (fixture_init(&fix) != 0)
		return TEST_FAILED;

	for (uint32_t idx = 0; idx < 16; ++idx) {
		char buf[KEY_SIZE];
		snprintf(buf, KEY_SIZE, "key-%u", idx * 7 + 3);
		set_name(idx, buf);
		TEST_ASSERT(
			str_index_insert(
				&fix.index,
				names[idx],
				KEY_SIZE,
				idx,
				read_name,
				NULL
			) == 0,
			"insert failed at %u",
			idx
		);
	}

	for (uint32_t idx = 0; idx < 16; ++idx) {
		uint32_t result = str_index_lookup(
			&fix.index, names[idx], KEY_SIZE, read_name, NULL
		);
		TEST_ASSERT_EQUAL(idx, result, "distinct key lookup %u", idx);
	}

	fixture_fini(&fix);
	return 0;
}

// Inserts enough entries to force several expansions, then confirms every
// stored value is still reachable after the rehashes.
static int
test_grow_and_rehash(void) {
	struct fixture fix;
	if (fixture_init(&fix) != 0)
		return TEST_FAILED;

	for (uint32_t idx = 0; idx < NAME_COUNT; ++idx) {
		char buf[KEY_SIZE];
		snprintf(buf, KEY_SIZE, "entry-%u", idx);
		set_name(idx, buf);
		TEST_ASSERT(
			str_index_insert(
				&fix.index,
				names[idx],
				KEY_SIZE,
				idx,
				read_name,
				NULL
			) == 0,
			"insert failed at %u",
			idx
		);
	}
	TEST_ASSERT(
		fix.index.hash_index.count == NAME_COUNT,
		"count must reach NAME_COUNT"
	);
	TEST_ASSERT(
		fix.index.hash_index.capacity >= NAME_COUNT,
		"capacity must have grown past NAME_COUNT"
	);

	for (uint32_t idx = 0; idx < NAME_COUNT; ++idx) {
		uint32_t result = str_index_lookup(
			&fix.index, names[idx], KEY_SIZE, read_name, NULL
		);
		TEST_ASSERT_EQUAL(idx, result, "post-grow lookup %u", idx);
	}

	uint32_t miss = str_index_lookup(
		&fix.index, "entry--1", KEY_SIZE, read_name, NULL
	);
	TEST_ASSERT_EQUAL(STR_INDEX_INVALID, miss, "absent key must miss");

	fixture_fini(&fix);
	return 0;
}

// A key that is a string prefix of a stored key must not match it: strncmp is
// bounded by key_size but stops at NUL, so distinct names stay distinct.
static int
test_prefix_distinct(void) {
	struct fixture fix;
	if (fixture_init(&fix) != 0)
		return TEST_FAILED;

	set_name(0, "port");
	set_name(1, "port-in");
	set_name(2, "port-out");

	for (uint32_t idx = 0; idx < 3; ++idx) {
		TEST_ASSERT(
			str_index_insert(
				&fix.index,
				names[idx],
				KEY_SIZE,
				idx,
				read_name,
				NULL
			) == 0,
			"insert failed at %u",
			idx
		);
	}

	for (uint32_t idx = 0; idx < 3; ++idx) {
		uint32_t result = str_index_lookup(
			&fix.index, names[idx], KEY_SIZE, read_name, NULL
		);
		TEST_ASSERT_EQUAL(
			idx, result, "prefix-distinct lookup %u", idx
		);
	}

	fixture_fini(&fix);
	return 0;
}

// Confirms that str_index_fini is idempotent and safe to call twice.
static int
test_fini_idempotent(void) {
	struct fixture fix;
	if (fixture_init(&fix) != 0)
		return TEST_FAILED;

	set_name(0, "solo");
	TEST_ASSERT(
		str_index_insert(
			&fix.index, names[0], KEY_SIZE, 0, read_name, NULL
		) == 0,
		"insert failed"
	);

	str_index_fini(&fix.index);
	str_index_fini(&fix.index);

	TEST_ASSERT(
		fix.index.hash_index.count == 0, "count must be 0 after fini"
	);
	TEST_ASSERT(
		fix.index.hash_index.capacity == 0,
		"capacity must be 0 after fini"
	);

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
	if (test_distinct_keys() != 0) {
		LOG(ERROR, "test_distinct_keys failed");
		return -1;
	}
	if (test_grow_and_rehash() != 0) {
		LOG(ERROR, "test_grow_and_rehash failed");
		return -1;
	}
	if (test_prefix_distinct() != 0) {
		LOG(ERROR, "test_prefix_distinct failed");
		return -1;
	}
	if (test_fini_idempotent() != 0) {
		LOG(ERROR, "test_fini_idempotent failed");
		return -1;
	}

	LOG(INFO, "str_index tests: OK");
	return 0;
}
