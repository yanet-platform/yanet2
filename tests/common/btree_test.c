#include "common/btree/u16.h"
#include "common/btree/u32.h"
#include "common/btree/u64.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////
// Helper function for setting up test allocator
////////////////////////////////////////////////////////////////////////////////

static int
setup_allocator(
	struct block_allocator *ba,
	struct memory_context *mctx,
	void **raw_mem,
	size_t size
) {
	TEST_ASSERT(
		block_allocator_init(ba) == 0, "block_allocator_init failed"
	);

	*raw_mem = malloc(size);
	TEST_ASSERT(*raw_mem != NULL, "failed to allocate test arena");

	block_allocator_put_arena(ba, *raw_mem, size);

	TEST_ASSERT(
		memory_context_init(mctx, "btree_test", ba) == 0,
		"memory_context_init failed"
	);

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Basic initialization and cleanup (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_init_free() {
	LOG(INFO, "Test: btree_u32 init and free");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t data[] = {1, 5, 10, 15, 20, 25, 30};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// Verify the tree was initialized
	TEST_ASSERT(tree.array.size > 0, "btree array size should be > 0");

	btree_u32_free(&tree);

	// Verify cleanup (array should be zeroed)
	TEST_ASSERT_EQUAL(tree.array.size, 0, "btree not properly freed");

	free(raw_mem);
	LOG(INFO, "✓ Basic u32 init/free test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Empty tree (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_empty() {
	LOG(INFO, "Test: empty btree_u32");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t *data = NULL;
	size_t n = 0;

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "empty btree_u32 initialization failed");

	btree_u32_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Empty u32 tree test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Single element (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_single_element() {
	LOG(INFO, "Test: single element btree_u32");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t data[] = {42};
	size_t n = 1;

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "single element btree_u32 initialization failed"
	);

	// Test lower_bound
	size_t idx = btree_u32_lower_bound(&tree, 42);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(42) should return 0");

	idx = btree_u32_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u32_lower_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, 1, "lower_bound(100) should return 1 (past end)"
	);

	// Test upper_bound
	idx = btree_u32_upper_bound(&tree, 42);
	TEST_ASSERT_EQUAL(idx, 1, "upper_bound(42) should return 1");

	idx = btree_u32_upper_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "upper_bound(0) should return 0");

	btree_u32_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Single element u32 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: LOWER_BOUND with uint32_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_lower_bound() {
	LOG(INFO, "Test: btree_u32_lower_bound");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// Test exact matches
	size_t idx = btree_u32_lower_bound(&tree, 10);
	TEST_ASSERT_EQUAL(idx, 2, "lower_bound(10) should return 2");

	idx = btree_u32_lower_bound(&tree, 15);
	TEST_ASSERT_EQUAL(idx, 3, "lower_bound(15) should return 3");

	idx = btree_u32_lower_bound(&tree, 40);
	TEST_ASSERT_EQUAL(idx, 8, "lower_bound(40) should return 8");

	// Test values between elements
	idx = btree_u32_lower_bound(&tree, 12);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(12) should return 3 (element 15)"
	);

	idx = btree_u32_lower_bound(&tree, 27);
	TEST_ASSERT_EQUAL(
		idx, 6, "lower_bound(27) should return 6 (element 30)"
	);

	// Test boundary cases
	idx = btree_u32_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u32_lower_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, n, "lower_bound(100) should return n (past end)"
	);

	btree_u32_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ btree_u32_lower_bound test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: UPPER_BOUND with uint32_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_upper_bound() {
	LOG(INFO, "Test: btree_u32_upper_bound");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// Test exact matches
	size_t idx = btree_u32_upper_bound(&tree, 1);
	TEST_ASSERT_EQUAL(idx, 1, "upper_bound(1) should return 1");

	idx = btree_u32_upper_bound(&tree, 15);
	TEST_ASSERT_EQUAL(idx, 4, "upper_bound(15) should return 4");

	idx = btree_u32_upper_bound(&tree, 40);
	TEST_ASSERT_EQUAL(idx, n, "upper_bound(40) should return n (past end)");

	// Test values between elements
	idx = btree_u32_upper_bound(&tree, 12);
	TEST_ASSERT_EQUAL(
		idx, 3, "upper_bound(12) should return 3 (element 15)"
	);

	idx = btree_u32_upper_bound(&tree, 27);
	TEST_ASSERT_EQUAL(
		idx, 6, "upper_bound(27) should return 6 (element 30)"
	);

	// Test boundary cases
	idx = btree_u32_upper_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "upper_bound(0) should return 0");

	idx = btree_u32_upper_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, n, "upper_bound(100) should return n (past end)"
	);

	btree_u32_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ btree_u32_upper_bound test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Basic initialization and cleanup (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_init_free() {
	LOG(INFO, "Test: btree_u16 init and free");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {1, 5, 10, 15, 20, 25, 30};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// Verify the tree was initialized
	TEST_ASSERT(tree.array.size > 0, "btree array size should be > 0");

	btree_u16_free(&tree);

	// Verify cleanup (array should be zeroed)
	TEST_ASSERT_EQUAL(tree.array.size, 0, "btree not properly freed");

	free(raw_mem);
	LOG(INFO, "✓ Basic u16 init/free test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Empty tree (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_empty() {
	LOG(INFO, "Test: empty btree_u16");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t *data = NULL;
	size_t n = 0;

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "empty btree_u16 initialization failed");

	btree_u16_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Empty u16 tree test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Single element (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_single_element() {
	LOG(INFO, "Test: single element btree_u16");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {42};
	size_t n = 1;

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "single element btree_u16 initialization failed"
	);

	// Test lower_bound
	size_t idx = btree_u16_lower_bound(&tree, 42);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(42) should return 0");

	idx = btree_u16_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u16_lower_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, 1, "lower_bound(100) should return 1 (past end)"
	);

	// Test upper_bound
	idx = btree_u16_upper_bound(&tree, 42);
	TEST_ASSERT_EQUAL(idx, 1, "upper_bound(42) should return 1");

	idx = btree_u16_upper_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "upper_bound(0) should return 0");

	btree_u16_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Single element u16 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: LOWER_BOUND with uint16_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_lower_bound() {
	LOG(INFO, "Test: btree_u16_lower_bound");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// Test exact matches
	size_t idx = btree_u16_lower_bound(&tree, 10);
	TEST_ASSERT_EQUAL(idx, 2, "lower_bound(10) should return 2");

	idx = btree_u16_lower_bound(&tree, 15);
	TEST_ASSERT_EQUAL(idx, 3, "lower_bound(15) should return 3");

	idx = btree_u16_lower_bound(&tree, 40);
	TEST_ASSERT_EQUAL(idx, 8, "lower_bound(40) should return 8");

	// Test values between elements
	idx = btree_u16_lower_bound(&tree, 12);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(12) should return 3 (element 15)"
	);

	idx = btree_u16_lower_bound(&tree, 27);
	TEST_ASSERT_EQUAL(
		idx, 6, "lower_bound(27) should return 6 (element 30)"
	);

	// Test boundary cases
	idx = btree_u16_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u16_lower_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, n, "lower_bound(100) should return n (past end)"
	);

	btree_u16_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ btree_u16_lower_bound test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: UPPER_BOUND with uint16_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_upper_bound() {
	LOG(INFO, "Test: btree_u16_upper_bound");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// Test exact matches
	size_t idx = btree_u16_upper_bound(&tree, 1);
	TEST_ASSERT_EQUAL(idx, 1, "upper_bound(1) should return 1");

	idx = btree_u16_upper_bound(&tree, 15);
	TEST_ASSERT_EQUAL(idx, 4, "upper_bound(15) should return 4");

	idx = btree_u16_upper_bound(&tree, 40);
	TEST_ASSERT_EQUAL(idx, n, "upper_bound(40) should return n (past end)");

	// Test values between elements
	idx = btree_u16_upper_bound(&tree, 12);
	TEST_ASSERT_EQUAL(
		idx, 3, "upper_bound(12) should return 3 (element 15)"
	);

	idx = btree_u16_upper_bound(&tree, 27);
	TEST_ASSERT_EQUAL(
		idx, 6, "upper_bound(27) should return 6 (element 30)"
	);

	// Test boundary cases
	idx = btree_u16_upper_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "upper_bound(0) should return 0");

	idx = btree_u16_upper_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, n, "upper_bound(100) should return n (past end)"
	);

	btree_u16_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ btree_u16_upper_bound test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Large dataset (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_large_dataset() {
	LOG(INFO, "Test: btree_u16 with large dataset");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 28; // 256 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 1000;
	uint16_t *data = malloc(n * sizeof(uint16_t));
	TEST_ASSERT_NOT_NULL(data, "failed to allocate test data");

	// Create sorted array: 0, 10, 20, 30, ...
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 10;
	}

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// Test various searches
	size_t idx = btree_u16_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u16_lower_bound(&tree, 5000);
	TEST_ASSERT_EQUAL(idx, 500, "lower_bound(5000) should return 500");

	idx = btree_u16_lower_bound(&tree, 9995);
	TEST_ASSERT_EQUAL(idx, n, "lower_bound(9995) should return n");

	idx = btree_u16_upper_bound(&tree, 4990);
	TEST_ASSERT_EQUAL(idx, 500, "upper_bound(4990) should return 500");

	// Test search for values between elements
	idx = btree_u16_lower_bound(&tree, 2345);
	TEST_ASSERT_EQUAL(
		idx, 235, "lower_bound(2345) should return 235 (element 2350)"
	);

	btree_u16_free(&tree);
	free(data);
	free(raw_mem);

	LOG(INFO, "✓ Large dataset u16 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Duplicate values (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_duplicates() {
	LOG(INFO, "Test: btree_u16 with duplicate values");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {1, 5, 5, 5, 10, 10, 15, 20, 20, 20, 20, 25};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// lower_bound should return first occurrence
	size_t idx = btree_u16_lower_bound(&tree, 5);
	TEST_ASSERT(
		idx >= 1 && idx <= 3,
		"lower_bound(5) should return index of first 5"
	);

	idx = btree_u16_lower_bound(&tree, 20);
	TEST_ASSERT(
		idx >= 7 && idx <= 10,
		"lower_bound(20) should return index of first 20"
	);

	// upper_bound should return past last occurrence
	idx = btree_u16_upper_bound(&tree, 5);
	TEST_ASSERT(idx >= 4, "upper_bound(5) should return index past last 5");

	idx = btree_u16_upper_bound(&tree, 20);
	TEST_ASSERT(
		idx >= 11, "upper_bound(20) should return index past last 20"
	);

	btree_u16_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Duplicate values u16 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Sequential searches (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_sequential_searches() {
	LOG(INFO, "Test: btree_u16 sequential searches");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {10, 20, 30, 40, 50, 60, 70, 80, 90, 100};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// Perform multiple searches to test consistency
	for (int i = 0; i < 3; i++) {
		size_t idx = btree_u16_lower_bound(&tree, 45);
		TEST_ASSERT_EQUAL(
			idx, 4, "lower_bound(45) should consistently return 4"
		);

		idx = btree_u16_upper_bound(&tree, 70);
		TEST_ASSERT_EQUAL(
			idx, 7, "upper_bound(70) should consistently return 7"
		);
	}

	btree_u16_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Sequential searches u16 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Boundary values (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_boundary_values() {
	LOG(INFO, "Test: btree_u16 boundary values");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// Test minimum value
	size_t idx = btree_u16_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	// Test maximum value
	idx = btree_u16_lower_bound(&tree, 9);
	TEST_ASSERT_EQUAL(idx, 9, "lower_bound(9) should return 9");

	idx = btree_u16_upper_bound(&tree, 9);
	TEST_ASSERT_EQUAL(idx, n, "upper_bound(9) should return n");

	btree_u16_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Boundary values u16 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Power of 2 sizes (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_power_of_2_sizes() {
	LOG(INFO, "Test: btree_u16 power of 2 sizes");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	// Test with 32 elements (power of 2, matches block size)
	uint16_t data32[32];
	for (size_t i = 0; i < 32; i++) {
		data32[i] = i * 5;
	}

	struct btree_u16 tree32;
	int ret = btree_u16_init(&tree32, data32, 32, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree_u16 initialization with 32 elements failed"
	);

	size_t idx = btree_u16_lower_bound(&tree32, 77);
	TEST_ASSERT_EQUAL(
		idx, 16, "lower_bound(77) should return 16 (element 80)"
	);

	btree_u16_free(&tree32);

	// Test with 64 elements (power of 2)
	uint16_t data64[64];
	for (size_t i = 0; i < 64; i++) {
		data64[i] = i * 2;
	}

	struct btree_u16 tree64;
	ret = btree_u16_init(&tree64, data64, 64, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree_u16 initialization with 64 elements failed"
	);

	idx = btree_u16_lower_bound(&tree64, 77);
	TEST_ASSERT_EQUAL(
		idx, 39, "lower_bound(77) should return 39 (element 78)"
	);

	for (size_t i = 0; i < 64; ++i) {
		idx = btree_u16_lower_bound(&tree64, i * 2);
		TEST_ASSERT_EQUAL(
			idx,
			i,
			"lower_bound(%d) should return %zu (element %d)",
			(uint16_t)(i * 2),
			i,
			(uint16_t)(i * 2)
		);

		idx = btree_u16_upper_bound(&tree64, i * 2);
		TEST_ASSERT_EQUAL(
			idx,
			i + 1,
			"upper_bound(%d) should return %zu (element %d)",
			(uint16_t)(i * 2),
			i + 1,
			(uint16_t)(i * 2)
		);
	}

	btree_u16_free(&tree64);

	free(raw_mem);
	LOG(INFO, "✓ Power of 2 sizes u16 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Various sizes (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_various_n(size_t n) {
	LOG(INFO, "Test: btree_u16 with n=%zu", n);

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t *data = malloc(n * sizeof(uint16_t));
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 2;
	}

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT(
		ret == 0, "btree_u16 initialization with %zu elements failed", n
	);

	for (uint16_t i = 0; i < 2 * n && i < 65535; i++) {
		int idx = btree_u16_lower_bound(&tree, i);
		TEST_ASSERT(
			idx == (int)(i + 1) / 2,
			"lower_bound(%d) should return %d (element %d)",
			(uint16_t)i,
			(i + 1) / 2,
			((i + 1) / 2) * 2
		);
	}

	for (uint16_t i = 0; i < 2 * n && i < 65535; ++i) {
		int idx = btree_u16_upper_bound(&tree, i);
		TEST_ASSERT(
			idx == (int)i / 2 + 1,
			"upper_bound(%d) should return %d (element %d)",
			(uint16_t)i,
			i / 2 + 1,
			(i / 2 + 1) * 2
		);
	}

	btree_u16_free(&tree);
	free(data);
	free(raw_mem);

	LOG(INFO, "✓ btree_u16 n=%zu test passed", n);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Batch upper_bounds with uint16_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_upper_bounds_batch() {
	LOG(INFO, "Test: btree_u16_upper_bounds batch");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// Test batch search with multiple values
	uint16_t search_values[] = {1, 10, 15, 27, 40, 100};
	uint32_t results[6];
	size_t count = btree_u16_upper_bounds(&tree, search_values, 6, results);
	TEST_ASSERT_EQUAL(count, 6, "upper_bounds should process 6 values");

	// Verify results
	TEST_ASSERT_EQUAL(results[0], 1, "upper_bound(1) should return 1");
	TEST_ASSERT_EQUAL(results[1], 3, "upper_bound(10) should return 3");
	TEST_ASSERT_EQUAL(results[2], 4, "upper_bound(15) should return 4");
	TEST_ASSERT_EQUAL(results[3], 6, "upper_bound(27) should return 6");
	TEST_ASSERT_EQUAL(results[4], n, "upper_bound(40) should return n");
	TEST_ASSERT_EQUAL(results[5], n, "upper_bound(100) should return n");

	// Test with single value
	uint16_t single_value = 20;
	uint32_t single_result;
	count = btree_u16_upper_bounds(&tree, &single_value, 1, &single_result);
	TEST_ASSERT_EQUAL(count, 1, "upper_bounds should process 1 value");
	TEST_ASSERT_EQUAL(single_result, 5, "upper_bound(20) should return 5");

	btree_u16_free(&tree);
	free(raw_mem);
	LOG(INFO, "✓ btree_u16_upper_bounds batch test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Batch upper_bounds with large batch (uint16_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u16_upper_bounds_large_batch() {
	LOG(INFO, "Test: btree_u16_upper_bounds large batch");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 100;
	uint16_t *data = malloc(n * sizeof(uint16_t));
	TEST_ASSERT_NOT_NULL(data, "failed to allocate test data");

	// Create sorted array: 0, 10, 20, 30, ...
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 10;
	}

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	// Test with 32 values (max batch size)
	uint16_t search_values[32];
	uint32_t results[32];
	for (size_t i = 0; i < 32; i++) {
		search_values[i] = i * 30 + 5; // 5, 35, 65, 95, ...
	}

	size_t count =
		btree_u16_upper_bounds(&tree, search_values, 32, results);
	TEST_ASSERT_EQUAL(count, 32, "upper_bounds should process 32 values");

	// Verify some results
	TEST_ASSERT_EQUAL(
		results[0], 1, "upper_bound(5) should return 1 (element 10)"
	);
	TEST_ASSERT_EQUAL(
		results[1], 4, "upper_bound(35) should return 4 (element 40)"
	);

	btree_u16_free(&tree);
	free(data);
	free(raw_mem);
	LOG(INFO, "✓ btree_u16_upper_bounds large batch test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: uint64_t basic operations
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_basic() {
	LOG(INFO, "Test: btree_u64 basic operations");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	size_t idx = btree_u64_lower_bound(&tree, 3500);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(3500) should return 3 (element 4000)"
	);

	idx = btree_u64_upper_bound(&tree, 5000);
	TEST_ASSERT_EQUAL(
		idx, 5, "upper_bound(5000) should return 5 (element 6000)"
	);

	btree_u64_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ btree_u64 basic test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Basic initialization and cleanup (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_init_free() {
	LOG(INFO, "Test: btree_u64 init and free");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {1, 5, 10, 15, 20, 25, 30};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// Verify the tree was initialized
	TEST_ASSERT(tree.array.size > 0, "btree array size should be > 0");

	btree_u64_free(&tree);

	// Verify cleanup (array should be zeroed)
	TEST_ASSERT_EQUAL(tree.array.size, 0, "btree not properly freed");

	free(raw_mem);
	LOG(INFO, "✓ Basic u64 init/free test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Empty tree (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_empty() {
	LOG(INFO, "Test: empty btree_u64");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t *data = NULL;
	size_t n = 0;

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "empty btree_u64 initialization failed");

	btree_u64_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Empty u64 tree test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Single element (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_single_element() {
	LOG(INFO, "Test: single element btree_u64");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {42};
	size_t n = 1;

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "single element btree_u64 initialization failed"
	);

	// Test lower_bound
	size_t idx = btree_u64_lower_bound(&tree, 42);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(42) should return 0");

	idx = btree_u64_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u64_lower_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, 1, "lower_bound(100) should return 1 (past end)"
	);

	// Test upper_bound
	idx = btree_u64_upper_bound(&tree, 42);
	TEST_ASSERT_EQUAL(idx, 1, "upper_bound(42) should return 1");

	idx = btree_u64_upper_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "upper_bound(0) should return 0");

	btree_u64_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Single element u64 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: LOWER_BOUND with uint64_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_lower_bound() {
	LOG(INFO, "Test: btree_u64_lower_bound");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// Test exact matches
	size_t idx = btree_u64_lower_bound(&tree, 10);
	TEST_ASSERT_EQUAL(idx, 2, "lower_bound(10) should return 2");

	idx = btree_u64_lower_bound(&tree, 15);
	TEST_ASSERT_EQUAL(idx, 3, "lower_bound(15) should return 3");

	idx = btree_u64_lower_bound(&tree, 40);
	TEST_ASSERT_EQUAL(idx, 8, "lower_bound(40) should return 8");

	// Test values between elements
	idx = btree_u64_lower_bound(&tree, 12);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(12) should return 3 (element 15)"
	);

	idx = btree_u64_lower_bound(&tree, 27);
	TEST_ASSERT_EQUAL(
		idx, 6, "lower_bound(27) should return 6 (element 30)"
	);

	// Test boundary cases
	idx = btree_u64_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u64_lower_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, n, "lower_bound(100) should return n (past end)"
	);

	btree_u64_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ btree_u64_lower_bound test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: UPPER_BOUND with uint64_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_upper_bound() {
	LOG(INFO, "Test: btree_u64_upper_bound");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// Test exact matches
	size_t idx = btree_u64_upper_bound(&tree, 1);
	TEST_ASSERT_EQUAL(idx, 1, "upper_bound(1) should return 1");

	idx = btree_u64_upper_bound(&tree, 15);
	TEST_ASSERT_EQUAL(idx, 4, "upper_bound(15) should return 4");

	idx = btree_u64_upper_bound(&tree, 40);
	TEST_ASSERT_EQUAL(idx, n, "upper_bound(40) should return n (past end)");

	// Test values between elements
	idx = btree_u64_upper_bound(&tree, 12);
	TEST_ASSERT_EQUAL(
		idx, 3, "upper_bound(12) should return 3 (element 15)"
	);

	idx = btree_u64_upper_bound(&tree, 27);
	TEST_ASSERT_EQUAL(
		idx, 6, "upper_bound(27) should return 6 (element 30)"
	);

	// Test boundary cases
	idx = btree_u64_upper_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "upper_bound(0) should return 0");

	idx = btree_u64_upper_bound(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, n, "upper_bound(100) should return n (past end)"
	);

	btree_u64_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ btree_u64_upper_bound test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Large dataset (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_large_dataset() {
	LOG(INFO, "Test: btree_u64 with large dataset");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 28; // 256 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 1000;
	uint64_t *data = malloc(n * sizeof(uint64_t));
	TEST_ASSERT_NOT_NULL(data, "failed to allocate test data");

	// Create sorted array: 0, 10, 20, 30, ...
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 10;
	}

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// Test various searches
	size_t idx = btree_u64_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u64_lower_bound(&tree, 5000);
	TEST_ASSERT_EQUAL(idx, 500, "lower_bound(5000) should return 500");

	idx = btree_u64_lower_bound(&tree, 9995);
	TEST_ASSERT_EQUAL(idx, n, "lower_bound(9995) should return n");

	idx = btree_u64_upper_bound(&tree, 4990);
	TEST_ASSERT_EQUAL(idx, 500, "upper_bound(4990) should return 500");

	// Test search for values between elements
	idx = btree_u64_lower_bound(&tree, 2345);
	TEST_ASSERT_EQUAL(
		idx, 235, "lower_bound(2345) should return 235 (element 2350)"
	);

	btree_u64_free(&tree);
	free(data);
	free(raw_mem);

	LOG(INFO, "✓ Large dataset u64 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Duplicate values (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_duplicates() {
	LOG(INFO, "Test: btree_u64 with duplicate values");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {1, 5, 5, 5, 10, 10, 15, 20, 20, 20, 20, 25};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// lower_bound should return first occurrence
	size_t idx = btree_u64_lower_bound(&tree, 5);
	TEST_ASSERT(
		idx >= 1 && idx <= 3,
		"lower_bound(5) should return index of first 5"
	);

	idx = btree_u64_lower_bound(&tree, 20);
	TEST_ASSERT(
		idx >= 7 && idx <= 10,
		"lower_bound(20) should return index of first 20"
	);

	// upper_bound should return past last occurrence
	idx = btree_u64_upper_bound(&tree, 5);
	TEST_ASSERT(idx >= 4, "upper_bound(5) should return index past last 5");

	idx = btree_u64_upper_bound(&tree, 20);
	TEST_ASSERT(
		idx >= 11, "upper_bound(20) should return index past last 20"
	);

	btree_u64_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Duplicate values u64 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Sequential searches (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_sequential_searches() {
	LOG(INFO, "Test: btree_u64 sequential searches");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {10, 20, 30, 40, 50, 60, 70, 80, 90, 100};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// Perform multiple searches to test consistency
	for (int i = 0; i < 3; i++) {
		size_t idx = btree_u64_lower_bound(&tree, 45);
		TEST_ASSERT_EQUAL(
			idx, 4, "lower_bound(45) should consistently return 4"
		);

		idx = btree_u64_upper_bound(&tree, 70);
		TEST_ASSERT_EQUAL(
			idx, 7, "upper_bound(70) should consistently return 7"
		);
	}

	btree_u64_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Sequential searches u64 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Boundary values (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_boundary_values() {
	LOG(INFO, "Test: btree_u64 boundary values");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// Test minimum value
	size_t idx = btree_u64_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	// Test maximum value
	idx = btree_u64_lower_bound(&tree, 9);
	TEST_ASSERT_EQUAL(idx, 9, "lower_bound(9) should return 9");

	idx = btree_u64_upper_bound(&tree, 9);
	TEST_ASSERT_EQUAL(idx, n, "upper_bound(9) should return n");

	btree_u64_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Boundary values u64 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Power of 2 sizes (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_power_of_2_sizes() {
	LOG(INFO, "Test: btree_u64 power of 2 sizes");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	// Test with 8 elements (power of 2, matches block size)
	uint64_t data8[8];
	for (size_t i = 0; i < 8; i++) {
		data8[i] = i * 5;
	}

	struct btree_u64 tree8;
	int ret = btree_u64_init(&tree8, data8, 8, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree_u64 initialization with 8 elements failed"
	);

	size_t idx = btree_u64_lower_bound(&tree8, 17);
	TEST_ASSERT_EQUAL(
		idx, 4, "lower_bound(17) should return 4 (element 20)"
	);

	btree_u64_free(&tree8);

	// Test with 64 elements (power of 2)
	uint64_t data64[64];
	for (size_t i = 0; i < 64; i++) {
		data64[i] = i * 2;
	}

	struct btree_u64 tree64;
	ret = btree_u64_init(&tree64, data64, 64, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree_u64 initialization with 64 elements failed"
	);

	idx = btree_u64_lower_bound(&tree64, 77);
	TEST_ASSERT_EQUAL(
		idx, 39, "lower_bound(77) should return 39 (element 78)"
	);

	for (uint64_t i = 0; i < 64; ++i) {
		idx = btree_u64_lower_bound(&tree64, i * 2);
		TEST_ASSERT_EQUAL(
			idx,
			i,
			"lower_bound(%lu) should return %lu (element %lu)",
			i * 2,
			i,
			i * 2
		);

		idx = btree_u64_upper_bound(&tree64, i * 2);
		TEST_ASSERT_EQUAL(
			idx,
			i + 1,
			"upper_bound(%lu) should return %lu (element %lu)",
			i * 2,
			i + 1,
			i * 2
		);
	}

	btree_u64_free(&tree64);

	free(raw_mem);
	LOG(INFO, "✓ Power of 2 sizes u64 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Large dataset (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_large_dataset() {
	LOG(INFO, "Test: btree_u32 with large dataset");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 28; // 256 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 1000;
	uint32_t *data = malloc(n * sizeof(uint32_t));
	TEST_ASSERT_NOT_NULL(data, "failed to allocate test data");

	// Create sorted array: 0, 10, 20, 30, ...
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 10;
	}

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// Test various searches
	size_t idx = btree_u32_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = btree_u32_lower_bound(&tree, 5000);
	TEST_ASSERT_EQUAL(idx, 500, "lower_bound(5000) should return 500");

	idx = btree_u32_lower_bound(&tree, 9995);
	TEST_ASSERT_EQUAL(idx, n, "lower_bound(9995) should return n");

	idx = btree_u32_upper_bound(&tree, 4990);
	TEST_ASSERT_EQUAL(idx, 500, "upper_bound(4990) should return 500");

	// Test search for values between elements
	idx = btree_u32_lower_bound(&tree, 2345);
	TEST_ASSERT_EQUAL(
		idx, 235, "lower_bound(2345) should return 235 (element 2350)"
	);

	btree_u32_free(&tree);
	free(data);
	free(raw_mem);

	LOG(INFO, "✓ Large dataset u32 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Duplicate values (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_duplicates() {
	LOG(INFO, "Test: btree_u32 with duplicate values");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t data[] = {1, 5, 5, 5, 10, 10, 15, 20, 20, 20, 20, 25};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// lower_bound should return first occurrence
	size_t idx = btree_u32_lower_bound(&tree, 5);
	TEST_ASSERT(
		idx >= 1 && idx <= 3,
		"lower_bound(5) should return index of first 5"
	);

	idx = btree_u32_lower_bound(&tree, 20);
	TEST_ASSERT(
		idx >= 7 && idx <= 10,
		"lower_bound(20) should return index of first 20"
	);

	// upper_bound should return past last occurrence
	idx = btree_u32_upper_bound(&tree, 5);
	TEST_ASSERT(idx >= 4, "upper_bound(5) should return index past last 5");

	idx = btree_u32_upper_bound(&tree, 20);
	TEST_ASSERT(
		idx >= 11, "upper_bound(20) should return index past last 20"
	);

	btree_u32_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Duplicate values u32 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Sequential searches (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_sequential_searches() {
	LOG(INFO, "Test: btree_u32 sequential searches");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t data[] = {10, 20, 30, 40, 50, 60, 70, 80, 90, 100};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// Perform multiple searches to test consistency
	for (int i = 0; i < 3; i++) {
		size_t idx = btree_u32_lower_bound(&tree, 45);
		TEST_ASSERT_EQUAL(
			idx, 4, "lower_bound(45) should consistently return 4"
		);

		idx = btree_u32_upper_bound(&tree, 70);
		TEST_ASSERT_EQUAL(
			idx, 7, "upper_bound(70) should consistently return 7"
		);
	}

	btree_u32_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Sequential searches u32 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Boundary values (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_boundary_values() {
	LOG(INFO, "Test: btree_u32 boundary values");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t data[] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// Test minimum value
	size_t idx = btree_u32_lower_bound(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	// Test maximum value
	idx = btree_u32_lower_bound(&tree, 9);
	TEST_ASSERT_EQUAL(idx, 9, "lower_bound(9) should return 9");

	idx = btree_u32_upper_bound(&tree, 9);
	TEST_ASSERT_EQUAL(idx, n, "upper_bound(9) should return n");

	btree_u32_free(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Boundary values u32 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Power of 2 sizes (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_power_of_2_sizes() {
	LOG(INFO, "Test: btree_u32 power of 2 sizes");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	// Test with 16 elements (power of 2)
	uint32_t data16[16];
	for (size_t i = 0; i < 16; i++) {
		data16[i] = i * 5;
	}

	struct btree_u32 tree16;
	int ret = btree_u32_init(&tree16, data16, 16, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree_u32 initialization with 16 elements failed"
	);

	size_t idx = btree_u32_lower_bound(&tree16, 37);
	TEST_ASSERT_EQUAL(
		idx, 8, "lower_bound(37) should return 8 (element 40)"
	);

	btree_u32_free(&tree16);

	// Test with 64 elements (power of 2)
	uint32_t data64[64];
	for (size_t i = 0; i < 64; i++) {
		data64[i] = i * 2;
	}

	struct btree_u32 tree64;
	ret = btree_u32_init(&tree64, data64, 64, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree_u32 initialization with 64 elements failed"
	);

	idx = btree_u32_lower_bound(&tree64, 77);
	TEST_ASSERT_EQUAL(
		idx, 39, "lower_bound(77) should return 39 (element 78)"
	);

	for (uint32_t i = 0; i < 64; ++i) {
		idx = btree_u32_lower_bound(&tree64, i * 2);
		TEST_ASSERT_EQUAL(
			idx,
			i,
			"lower_bound(%d) should return %d (element %d)",
			(uint32_t)(i * 2),
			i,
			i * 2
		);

		idx = btree_u32_upper_bound(&tree64, i * 2);
		TEST_ASSERT_EQUAL(
			idx,
			i + 1,
			"upper_bound(%d) should return %d (element %d)",
			(uint32_t)(i * 2),
			i + 1,
			i * 2
		);
	}

	btree_u32_free(&tree64);

	free(raw_mem);
	LOG(INFO, "✓ Power of 2 sizes u32 test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Various sizes (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_various_n(size_t n) {
	LOG(INFO, "Test: btree_u32 with n=%zu", n);

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t *data = malloc(n * sizeof(uint32_t));
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 2;
	}

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT(
		ret == 0, "btree_u32 initialization with %zu elements failed", n
	);

	for (uint32_t i = 0; i < 2 * n; i++) {
		int idx = btree_u32_lower_bound(&tree, i);
		TEST_ASSERT(
			idx == (int)(i + 1) / 2,
			"lower_bound(%d) should return %d (element %d)",
			(uint32_t)i,
			(i + 1) / 2,
			((i + 1) / 2) * 2
		);
	}

	for (uint32_t i = 0; i < 2 * n; ++i) {
		int idx = btree_u32_upper_bound(&tree, i);
		TEST_ASSERT(
			idx == (int)i / 2 + 1,
			"upper_bound(%d) should return %d (element %d)",
			(uint32_t)i,
			i / 2 + 1,
			(i / 2 + 1) * 2
		);
	}

	btree_u32_free(&tree);
	free(data);
	free(raw_mem);

	LOG(INFO, "✓ btree_u32 n=%zu test passed", n);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Various sizes (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_various_n(size_t n) {
	LOG(INFO, "Test: btree_u64 with n=%zu", n);

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t *data = malloc(n * sizeof(uint64_t));
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 2;
	}

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT(
		ret == 0, "btree_u64 initialization with %zu elements failed", n
	);

	for (uint64_t i = 0; i < 2 * n; i++) {
		int idx = btree_u64_lower_bound(&tree, i);
		TEST_ASSERT(
			idx == (int)(i + 1) / 2,
			"lower_bound(%lu) should return %lu (element %lu)",
			i,
			(i + 1) / 2,
			((i + 1) / 2) * 2
		);
	}

	for (uint64_t i = 0; i < 2 * n; ++i) {
		int idx = btree_u64_upper_bound(&tree, i);
		TEST_ASSERT(
			idx == (int)i / 2 + 1,
			"upper_bound(%lu) should return %lu (element %lu)",
			i,
			i / 2 + 1,
			(i / 2 + 1) * 2
		);
	}

	btree_u64_free(&tree);
	free(data);
	free(raw_mem);

	LOG(INFO, "✓ btree_u64 n=%zu test passed", n);
	return TEST_SUCCESS;
}
////////////////////////////////////////////////////////////////////////////////
// Test: Batch upper_bounds with uint32_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_upper_bounds_batch() {
	LOG(INFO, "Test: btree_u32_upper_bounds batch");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint32_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// Test batch search with multiple values
	uint32_t search_values[] = {1, 10, 15, 27, 40, 100};
	uint32_t results[6];
	size_t count = btree_u32_upper_bounds(&tree, search_values, 6, results);
	TEST_ASSERT_EQUAL(count, 6, "upper_bounds should process 6 values");

	// Verify results
	TEST_ASSERT_EQUAL(results[0], 1, "upper_bound(1) should return 1");
	TEST_ASSERT_EQUAL(results[1], 3, "upper_bound(10) should return 3");
	TEST_ASSERT_EQUAL(results[2], 4, "upper_bound(15) should return 4");
	TEST_ASSERT_EQUAL(results[3], 6, "upper_bound(27) should return 6");
	TEST_ASSERT_EQUAL(results[4], n, "upper_bound(40) should return n");
	TEST_ASSERT_EQUAL(results[5], n, "upper_bound(100) should return n");

	// Test with single value
	uint32_t single_value = 20;
	uint32_t single_result;
	count = btree_u32_upper_bounds(&tree, &single_value, 1, &single_result);
	TEST_ASSERT_EQUAL(count, 1, "upper_bounds should process 1 value");
	TEST_ASSERT_EQUAL(single_result, 5, "upper_bound(20) should return 5");

	btree_u32_free(&tree);
	free(raw_mem);
	LOG(INFO, "✓ btree_u32_upper_bounds batch test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Batch upper_bounds with uint64_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_upper_bounds_batch() {
	LOG(INFO, "Test: btree_u64_upper_bounds batch");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint64_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// Test batch search with multiple values
	uint64_t search_values[] = {1, 10, 15, 27, 40, 100};
	uint32_t results[6];
	size_t count = btree_u64_upper_bounds(&tree, search_values, 6, results);
	TEST_ASSERT_EQUAL(count, 6, "upper_bounds should process 6 values");

	// Verify results
	TEST_ASSERT_EQUAL(results[0], 1, "upper_bound(1) should return 1");
	TEST_ASSERT_EQUAL(results[1], 3, "upper_bound(10) should return 3");
	TEST_ASSERT_EQUAL(results[2], 4, "upper_bound(15) should return 4");
	TEST_ASSERT_EQUAL(results[3], 6, "upper_bound(27) should return 6");
	TEST_ASSERT_EQUAL(results[4], n, "upper_bound(40) should return n");
	TEST_ASSERT_EQUAL(results[5], n, "upper_bound(100) should return n");

	// Test with single value
	uint64_t single_value = 20;
	uint32_t single_result;
	count = btree_u64_upper_bounds(&tree, &single_value, 1, &single_result);
	TEST_ASSERT_EQUAL(count, 1, "upper_bounds should process 1 value");
	TEST_ASSERT_EQUAL(single_result, 5, "upper_bound(20) should return 5");

	btree_u64_free(&tree);
	free(raw_mem);
	LOG(INFO, "✓ btree_u64_upper_bounds batch test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Batch upper_bounds with large batch (uint32_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u32_upper_bounds_large_batch() {
	LOG(INFO, "Test: btree_u32_upper_bounds large batch");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 100;
	uint32_t *data = malloc(n * sizeof(uint32_t));
	TEST_ASSERT_NOT_NULL(data, "failed to allocate test data");

	// Create sorted array: 0, 10, 20, 30, ...
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 10;
	}

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	// Test with 32 values (max batch size)
	uint32_t search_values[32];
	uint32_t results[32];
	for (size_t i = 0; i < 32; i++) {
		search_values[i] = i * 30 + 5; // 5, 35, 65, 95, ...
	}

	size_t count =
		btree_u32_upper_bounds(&tree, search_values, 32, results);
	TEST_ASSERT_EQUAL(count, 32, "upper_bounds should process 32 values");

	// Verify some results
	TEST_ASSERT_EQUAL(
		results[0], 1, "upper_bound(5) should return 1 (element 10)"
	);
	TEST_ASSERT_EQUAL(
		results[1], 4, "upper_bound(35) should return 4 (element 40)"
	);

	btree_u32_free(&tree);
	free(data);
	free(raw_mem);
	LOG(INFO, "✓ btree_u32_upper_bounds large batch test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Batch upper_bounds with large batch (uint64_t)
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_u64_upper_bounds_large_batch() {
	LOG(INFO, "Test: btree_u64_upper_bounds large batch");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 100;
	uint64_t *data = malloc(n * sizeof(uint64_t));
	TEST_ASSERT_NOT_NULL(data, "failed to allocate test data");

	// Create sorted array: 0, 10, 20, 30, ...
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 10;
	}

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	// Test with 32 values (max batch size)
	uint64_t search_values[32];
	uint32_t results[32];
	for (size_t i = 0; i < 32; i++) {
		search_values[i] = i * 30 + 5; // 5, 35, 65, 95, ...
	}

	size_t count =
		btree_u64_upper_bounds(&tree, search_values, 32, results);
	TEST_ASSERT_EQUAL(count, 32, "upper_bounds should process 32 values");

	// Verify some results
	TEST_ASSERT_EQUAL(
		results[0], 1, "upper_bound(5) should return 1 (element 10)"
	);
	TEST_ASSERT_EQUAL(
		results[1], 4, "upper_bound(35) should return 4 (element 40)"
	);

	btree_u64_free(&tree);
	free(data);
	free(raw_mem);
	LOG(INFO, "✓ btree_u64_upper_bounds large batch test passed");
	return TEST_SUCCESS;
}

static int
test_btree_u16_sentinel_high_bit() {
	LOG(INFO, "Test: btree_u16 sentinel with high-bit last element");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26;

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 65;
	uint16_t data[65];
	for (size_t i = 0; i < 64; ++i) {
		data[i] = (uint16_t)(i + 1);
	}
	const uint16_t high = 0xA000u;
	const uint16_t under_high = 0x5000u;
	data[64] = high;

	struct btree_u16 tree;
	int ret = btree_u16_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u16 initialization failed");

	size_t idx = btree_u16_lower_bound(&tree, under_high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u16_lower_bound(&tree, high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u16_upper_bound(&tree, under_high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u16_upper_bound(&tree, high);
	TEST_ASSERT_EQUAL(idx, n, "must return n");

	btree_u16_free(&tree);
	free(raw_mem);
	LOG(INFO, "✓ u16 high-bit sentinel test passed");
	return TEST_SUCCESS;
}

static int
test_btree_u32_sentinel_high_bit() {
	LOG(INFO, "Test: btree_u32 sentinel with high-bit last element");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26;

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 33;
	uint32_t data[33];
	for (size_t i = 0; i < 32; ++i) {
		data[i] = (uint32_t)(i + 1);
	}
	const uint32_t high = 0xA0000000u;
	const uint32_t under_high = 0x50000000u;
	data[32] = high;

	struct btree_u32 tree;
	int ret = btree_u32_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u32 initialization failed");

	size_t idx = btree_u32_lower_bound(&tree, under_high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u32_lower_bound(&tree, high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u32_upper_bound(&tree, under_high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u32_upper_bound(&tree, high);
	TEST_ASSERT_EQUAL(idx, n, "must return n");

	btree_u32_free(&tree);
	free(raw_mem);
	LOG(INFO, "✓ u32 high-bit sentinel test passed");
	return TEST_SUCCESS;
}

static int
test_btree_u64_sentinel_high_bit() {
	LOG(INFO, "Test: btree_u64 sentinel with high-bit last element");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26;

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	const size_t n = 17;
	uint64_t data[17];
	for (size_t i = 0; i < 16; ++i) {
		data[i] = (uint64_t)(i + 1);
	}
	const uint64_t high = 0xA000000000000000ULL;
	const uint64_t under_high = 0x5000000000000000ULL;
	data[16] = high;

	struct btree_u64 tree;
	int ret = btree_u64_init(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree_u64 initialization failed");

	size_t idx = btree_u64_lower_bound(&tree, under_high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u64_lower_bound(&tree, high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u64_upper_bound(&tree, under_high);
	TEST_ASSERT_EQUAL(idx, n - 1, "must return n-1");

	idx = btree_u64_upper_bound(&tree, high);
	TEST_ASSERT_EQUAL(idx, n, "must return n");

	btree_u64_free(&tree);
	free(raw_mem);
	LOG(INFO, "✓ u64 high-bit sentinel test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Main test runner
////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("info");

	LOG(INFO, "=== Starting btree test suite (new API) ===\n");

	int failed = 0;

	// Run uint16_t tests
	if (test_btree_u16_init_free() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_empty() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_single_element() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_lower_bound() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_upper_bound() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_large_dataset() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_duplicates() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_sequential_searches() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_boundary_values() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_power_of_2_sizes() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_upper_bounds_batch() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_upper_bounds_large_batch() != TEST_SUCCESS)
		failed++;
	if (test_btree_u16_sentinel_high_bit() != TEST_SUCCESS)
		failed++;

	// Run uint32_t tests
	if (test_btree_u32_init_free() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_empty() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_single_element() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_lower_bound() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_upper_bound() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_large_dataset() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_duplicates() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_sequential_searches() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_boundary_values() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_power_of_2_sizes() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_upper_bounds_batch() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_upper_bounds_large_batch() != TEST_SUCCESS)
		failed++;
	if (test_btree_u32_sentinel_high_bit() != TEST_SUCCESS)
		failed++;

	// Run uint64_t tests
	if (test_btree_u64_init_free() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_empty() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_single_element() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_lower_bound() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_upper_bound() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_basic() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_large_dataset() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_duplicates() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_sequential_searches() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_boundary_values() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_power_of_2_sizes() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_upper_bounds_batch() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_upper_bounds_large_batch() != TEST_SUCCESS)
		failed++;
	if (test_btree_u64_sentinel_high_bit() != TEST_SUCCESS)
		failed++;

	// Run various size tests
	size_t ns[] = {
		16,
		25,
		33,
		100,
		1000,
		555,
		1024,
		777,
		10000,
		333,
		64,
		1024,
		1 << 15,
		(1 << 14) - 1
	};
	for (size_t i = 0; i < sizeof(ns) / sizeof(size_t); i++) {
		if (test_btree_u16_various_n(ns[i]) != TEST_SUCCESS)
			failed++;
		if (test_btree_u32_various_n(ns[i]) != TEST_SUCCESS)
			failed++;
		if (test_btree_u64_various_n(ns[i]) != TEST_SUCCESS)
			failed++;
	}

	if (failed == 0) {
		LOG(INFO, "\n=== All btree tests passed! ===");
		return 0;
	} else {
		LOG(ERROR, "\n=== %d test(s) failed ===", failed);
		return 1;
	}
}