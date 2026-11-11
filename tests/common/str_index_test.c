/*
 * Entry accounting of the string index across expansions.
 *
 * An expansion re-inserts every surviving entry, and the index counts its
 * entries so the next expansion can size itself from them: the count
 * must reflect the entries once, not once per table they lived in. The
 * scenarios insert known keys across several expansion boundaries and
 * pin the capacity band and the resolution of every key afterwards.
 */

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/str_index.h"
#include "common/test_assert.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ARENA_SIZE (8u * 1024u * 1024u)
#define KEY_SIZE 32
#define RESOLVE_ENTRY_COUNT 16000

struct str_index_fixture {
	struct block_allocator allocator;
	struct memory_context memory_context;
	// Fixed-width keys, one per entry, in insertion order.
	char (*names)[KEY_SIZE];
	uint64_t count;
	void *arena;
};

// Resolves an entry to its key for the index's read callback.
static const char *
fixture_read_name(uint32_t index, const void *data) {
	const struct str_index_fixture *fixture =
		(const struct str_index_fixture *)data;
	return fixture->names[index];
}

// Prepares a fixture of the given entry count over a fresh arena.
static int
str_index_fixture_init(struct str_index_fixture *fixture, uint64_t count) {
	memset(fixture, 0, sizeof(*fixture));
	fixture->count = count;
	fixture->names = calloc(count, KEY_SIZE);
	TEST_ASSERT_NOT_NULL(fixture->names, "failed to allocate names");
	fixture->arena =
		aligned_alloc(MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN, ARENA_SIZE);
	TEST_ASSERT_NOT_NULL(fixture->arena, "failed to allocate arena");

	block_allocator_init(&fixture->allocator);
	block_allocator_put_arena(
		&fixture->allocator, fixture->arena, ARENA_SIZE
	);
	memory_context_init(
		&fixture->memory_context, "str-index-test", &fixture->allocator
	);
	return TEST_SUCCESS;
}

// Releases the fixture's arena and key storage.
static void
str_index_fixture_fini(struct str_index_fixture *fixture) {
	memory_context_fini(&fixture->memory_context);
	block_allocator_fini(&fixture->allocator);
	free(fixture->names);
	free(fixture->arena);
}

// Builds an index of the fixture's entries, one distinct key each.
static int
fixture_build(struct str_index_fixture *fixture, struct str_index *str_index) {
	TEST_ASSERT_SUCCESS(
		str_index_init(str_index, &fixture->memory_context),
		"failed to init the index"
	);
	for (uint64_t idx = 0; idx < fixture->count; ++idx) {
		snprintf(fixture->names[idx], KEY_SIZE, "entry-%" PRIu64, idx);
		if (str_index_insert(
			    str_index,
			    fixture->names[idx],
			    KEY_SIZE,
			    (uint32_t)idx,
			    fixture_read_name,
			    fixture
		    )) {
			TEST_ASSERT(
				false, "failed to insert entry %" PRIu64, idx
			);
		}
	}
	return TEST_SUCCESS;
}

// verifies that the capacity stays between four and eight slots per entry
// across several expansion boundaries
static int
run_capacity_band_test(void) {
	const uint64_t counts[] = {
		1, 2, 3, 5, 17, 1023, 1024, 1025, 4096, 16000
	};

	for (size_t idx = 0; idx < sizeof(counts) / sizeof(counts[0]); ++idx) {
		struct str_index_fixture fixture;
		struct str_index str_index;
		TEST_ASSERT_SUCCESS(
			str_index_fixture_init(&fixture, counts[idx]),
			"fixture init failed"
		);
		TEST_ASSERT_SUCCESS(
			fixture_build(&fixture, &str_index),
			"fixture build failed"
		);

		// The band holds for every entry count, the boundaries
		// included.
		uint64_t capacity = str_index.capacity;
		TEST_ASSERT(
			capacity >= counts[idx] * STR_INDEX_SPARSE_FACTOR,
			"capacity %" PRIu64
			" below the sparse floor for %" PRIu64 " entries",
			capacity,
			counts[idx]
		);
		TEST_ASSERT(
			capacity <= counts[idx] * STR_INDEX_SPARSE_FACTOR * 2,
			"capacity %" PRIu64
			" above the sparse ceiling for %" PRIu64 " entries",
			capacity,
			counts[idx]
		);

		str_index_fini(&str_index);
		str_index_fixture_fini(&fixture);
	}
	return TEST_SUCCESS;
}

// verifies that every inserted key resolves after several expansions and
// an absent key does not
static int
run_keys_resolve_after_expansion_test(void) {
	struct str_index_fixture fixture;
	struct str_index str_index;
	TEST_ASSERT_SUCCESS(
		str_index_fixture_init(&fixture, RESOLVE_ENTRY_COUNT),
		"fixture init failed"
	);
	TEST_ASSERT_SUCCESS(
		fixture_build(&fixture, &str_index), "fixture build failed"
	);

	for (uint64_t idx = 0; idx < RESOLVE_ENTRY_COUNT; ++idx) {
		TEST_ASSERT(
			str_index_lookup(
				&str_index,
				fixture.names[idx],
				KEY_SIZE,
				fixture_read_name,
				&fixture
			) == idx,
			"entry %" PRIu64 " no longer resolves",
			idx
		);
	}
	TEST_ASSERT(
		str_index_lookup(
			&str_index,
			"entry-missing",
			KEY_SIZE,
			fixture_read_name,
			&fixture
		) == STR_INDEX_INVALID,
		"an absent key resolved"
	);

	str_index_fini(&str_index);
	str_index_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

int
main(void) {
	int results[] = {
		run_capacity_band_test(),
		run_keys_resolve_after_expansion_test(),
	};
	for (size_t idx = 0; idx < sizeof(results) / sizeof(results[0]);
	     ++idx) {
		if (results[idx] != TEST_SUCCESS) {
			return EXIT_FAILURE;
		}
	}
	return EXIT_SUCCESS;
}
