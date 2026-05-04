#include "common/flatmap.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static int
setup_allocator(struct block_allocator *ba, void **raw_mem, size_t size) {
	TEST_ASSERT(
		block_allocator_init(ba) == 0, "block_allocator_init failed"
	);

	*raw_mem = malloc(size);
	TEST_ASSERT(*raw_mem != NULL, "failed to allocate raw memory");

	block_allocator_put_arena(ba, *raw_mem, size);
	return TEST_SUCCESS;
}

static int
test_zero_count(void) {
	LOG(INFO, "Test zero count...");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 20;
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup allocator"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "flatmap", &ba) == 0,
		"init memory context"
	);

	struct flatmap map;
	int res = flatmap_build(
		&map,
		&mctx,
		8,
		0,
		NULL,
		sizeof(uint32_t),
		NULL,
		sizeof(uint32_t),
		NULL
	);
	TEST_ASSERT(res == 0, "build flat map");

	uint32_t probes[] = {0, 1, 42, 0xDEADBEEFu};
	for (size_t idx = 0; idx < sizeof(probes) / sizeof(probes[0]); ++idx) {
		void *found = flatmap_lookup(
			&map, &probes[idx], sizeof(uint32_t), sizeof(uint32_t)
		);
		TEST_ASSERT_NULL(found, "lookup on empty map must return NULL");
	}

	flatmap_free(&map, &mctx, sizeof(uint32_t), sizeof(uint32_t));
	TEST_ASSERT_EQUAL(
		mctx.bfree_count,
		mctx.balloc_count,
		"memory context: free count != alloc count"
	);
	TEST_ASSERT_EQUAL(
		mctx.bfree_size,
		mctx.balloc_size,
		"memory context: free size != alloc size"
	);
	free(raw_mem);
	return TEST_SUCCESS;
}

static int
test_basic_lookup(void) {
	LOG(INFO, "Test basic lookup...");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 20;
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup allocator"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "flatmap", &ba) == 0,
		"init memory context"
	);

	const uint32_t keys[] = {1, 2, 3, 100, 200};
	const uint32_t values[] = {10, 20, 30, 1000, 2000};
	const size_t count = sizeof(keys) / sizeof(keys[0]);

	struct flatmap map;
	int res = flatmap_build(
		&map,
		&mctx,
		count * 2,
		count,
		keys,
		sizeof(uint32_t),
		values,
		sizeof(uint32_t),
		NULL
	);
	TEST_ASSERT(res == 0, "build flat map");

	for (size_t idx = 0; idx < count; ++idx) {
		uint32_t key = keys[idx];
		uint32_t *value = (uint32_t *)flatmap_lookup(
			&map, &key, sizeof(uint32_t), sizeof(uint32_t)
		);
		TEST_ASSERT_NOT_NULL(
			value, "lookup of present key %u returned NULL", key
		);
		TEST_ASSERT_EQUAL(
			*value, values[idx], "value mismatch for key %u", key
		);
	}

	const uint32_t missing[] = {0, 4, 99, 500, 0xFFFFFFFFu};
	for (size_t idx = 0; idx < sizeof(missing) / sizeof(missing[0]);
	     ++idx) {
		void *value = flatmap_lookup(
			&map, &missing[idx], sizeof(uint32_t), sizeof(uint32_t)
		);
		TEST_ASSERT_NULL(
			value,
			"lookup of missing key %u returned non-NULL",
			missing[idx]
		);
	}

	flatmap_free(&map, &mctx, sizeof(uint32_t), sizeof(uint32_t));
	TEST_ASSERT_EQUAL(
		mctx.bfree_count,
		mctx.balloc_count,
		"memory context: free count != alloc count"
	);
	TEST_ASSERT_EQUAL(
		mctx.bfree_size,
		mctx.balloc_size,
		"memory context: free size != alloc size"
	);
	free(raw_mem);
	return TEST_SUCCESS;
}

static int
test_duplicate_key(void) {
	LOG(INFO, "Test duplicate key...");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 20;
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup allocator"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "flatmap", &ba) == 0,
		"init memory context"
	);

	const uint32_t keys[] = {1, 2, 1, 100};
	const uint32_t values[] = {10, 20, 30, 1000};
	const size_t count = sizeof(keys) / sizeof(keys[0]);

	struct flatmap map;
	yanet_error *err = NULL;
	int res = flatmap_build(
		&map,
		&mctx,
		count * 2,
		count,
		keys,
		sizeof(uint32_t),
		values,
		sizeof(uint32_t),
		&err
	);
	TEST_ASSERT(res == -1, "build must reject a duplicate key");
	TEST_ASSERT_NOT_NULL(err, "build must report an error");

	yanet_error_free(err);

	TEST_ASSERT_EQUAL(
		mctx.bfree_count,
		mctx.balloc_count,
		"memory context: free count != alloc count"
	);
	TEST_ASSERT_EQUAL(
		mctx.bfree_size,
		mctx.balloc_size,
		"memory context: free size != alloc size"
	);
	free(raw_mem);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	if (test_zero_count() != TEST_SUCCESS) {
		return -1;
	}
	if (test_basic_lookup() != TEST_SUCCESS) {
		return -1;
	}
	if (test_duplicate_key() != TEST_SUCCESS) {
		return -1;
	}

	LOG(INFO, "All flatmap tests passed");
	return 0;
}