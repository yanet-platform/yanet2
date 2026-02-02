#include "common/btree.h"
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
// Test: Basic initialization and cleanup
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_init_free() {
	LOG(INFO, "Test: btree init and free");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Verify the tree was initialized
	TEST_ASSERT(tree.array.size > 0, "btree array size should be > 0");

	BTREE_FREE(&tree);

	// Verify cleanup (array should be zeroed)
	TEST_ASSERT_EQUAL(tree.array.size, 0, "btree not properly freed");

	free(raw_mem);
	LOG(INFO, "✓ Basic init/free test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Empty tree
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_empty() {
	LOG(INFO, "Test: empty btree");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "empty btree initialization failed");

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Empty tree test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Single element
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_single_element() {
	LOG(INFO, "Test: single element btree");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "single element btree initialization failed");

	// Test lower_bound
	size_t idx = BTREE_LOWER_BOUND(&tree, 42);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(42) should return 0");

	idx = BTREE_LOWER_BOUND(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = BTREE_LOWER_BOUND(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, 1, "lower_bound(100) should return 1 (past end)"
	);

	// Test upper_bound
	idx = BTREE_UPPER_BOUND(&tree, 42);
	TEST_ASSERT_EQUAL(idx, 1, "upper_bound(42) should return 1");

	idx = BTREE_UPPER_BOUND(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "upper_bound(0) should return 0");

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Single element test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: LOWER_BOUND with uint32_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_lower_bound_uint32() {
	LOG(INFO, "Test: BTREE_LOWER_BOUND with uint32_t");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Test exact matches
	size_t idx = BTREE_LOWER_BOUND(&tree, (uint32_t)10);
	TEST_ASSERT_EQUAL(idx, 2, "lower_bound(10) should return 2");

	idx = BTREE_LOWER_BOUND(&tree, (uint32_t)15);
	TEST_ASSERT_EQUAL(idx, 3, "lower_bound(15) should return 3");

	idx = BTREE_LOWER_BOUND(&tree, (uint32_t)40);
	TEST_ASSERT_EQUAL(idx, 8, "lower_bound(40) should return 8");

	// Test values between elements
	idx = BTREE_LOWER_BOUND(&tree, (uint32_t)12);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(12) should return 3 (element 15)"
	);

	idx = BTREE_LOWER_BOUND(&tree, (uint32_t)27);
	TEST_ASSERT_EQUAL(
		idx, 6, "lower_bound(27) should return 6 (element 30)"
	);

	// Test boundary cases
	idx = BTREE_LOWER_BOUND(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = BTREE_LOWER_BOUND(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, n, "lower_bound(100) should return n (past end)"
	);

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ LOWER_BOUND uint32_t test passed");
	return TEST_SUCCESS;
}

// ////////////////////////////////////////////////////////////////////////////////
// // Test: UPPER_BOUND with uint32_t
// ////////////////////////////////////////////////////////////////////////////////

static int
test_btree_upper_bound_uint32() {
	LOG(INFO, "Test: BTREE_UPPER_BOUND with uint32_t");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Test exact matches
	size_t idx = BTREE_UPPER_BOUND(&tree, 1);
	TEST_ASSERT_EQUAL(idx, 1, "upper_bound(1) should return 1");

	idx = BTREE_UPPER_BOUND(&tree, 15);
	TEST_ASSERT_EQUAL(idx, 4, "upper_bound(15) should return 4");

	idx = BTREE_UPPER_BOUND(&tree, 40);
	TEST_ASSERT_EQUAL(idx, n, "upper_bound(40) should return n (past end)");

	// Test values between elements
	idx = BTREE_UPPER_BOUND(&tree, 12);
	TEST_ASSERT_EQUAL(
		idx, 3, "upper_bound(12) should return 3 (element 15)"
	);

	idx = BTREE_UPPER_BOUND(&tree, 27);
	TEST_ASSERT_EQUAL(
		idx, 6, "upper_bound(27) should return 6 (element 30)"
	);

	// Test boundary cases
	idx = BTREE_UPPER_BOUND(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "upper_bound(0) should return 0");

	idx = BTREE_UPPER_BOUND(&tree, 100);
	TEST_ASSERT_EQUAL(
		idx, n, "upper_bound(100) should return n (past end)"
	);

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ UPPER_BOUND uint32_t test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Different data types - uint16_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_uint16() {
	LOG(INFO, "Test: btree with uint16_t");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB

	TEST_ASSERT(
		setup_allocator(&ba, &mctx, &raw_mem, arena_size) ==
			TEST_SUCCESS,
		"setup_allocator failed"
	);

	uint16_t data[] = {100, 200, 300, 400, 500, 600, 700, 800};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	size_t idx = BTREE_LOWER_BOUND(&tree, (uint16_t)350);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(350) should return 3 (element 400)"
	);

	idx = BTREE_UPPER_BOUND(&tree, (uint16_t)500);
	TEST_ASSERT_EQUAL(
		idx, 5, "upper_bound(500) should return 5 (element 600)"
	);

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ uint16_t test passed");
	return TEST_SUCCESS;
}

// ////////////////////////////////////////////////////////////////////////////////
// // Test: Different data types - uint64_t
// ////////////////////////////////////////////////////////////////////////////////

static int
test_btree_uint64() {
	LOG(INFO, "Test: btree with uint64_t");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	size_t idx = BTREE_LOWER_BOUND(&tree, (uint64_t)3500);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(3500) should return 3 (element 4000)"
	);

	idx = BTREE_UPPER_BOUND(&tree, (uint64_t)5000);
	TEST_ASSERT_EQUAL(
		idx, 5, "upper_bound(5000) should return 5 (element 6000)"
	);

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ uint64_t test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Large dataset
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_large_dataset() {
	LOG(INFO, "Test: btree with large dataset");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Test various searches
	size_t idx = BTREE_LOWER_BOUND(&tree, (uint32_t)0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = BTREE_LOWER_BOUND(&tree, (uint32_t)5000);
	TEST_ASSERT_EQUAL(idx, 500, "lower_bound(5000) should return 500");

	idx = BTREE_LOWER_BOUND(&tree, (uint32_t)9995);
	TEST_ASSERT_EQUAL(idx, n, "lower_bound(9995) should return n");

	idx = BTREE_UPPER_BOUND(&tree, (uint32_t)4990);
	TEST_ASSERT_EQUAL(idx, 500, "upper_bound(4990) should return 500");

	// Test search for values between elements
	idx = BTREE_LOWER_BOUND(&tree, (uint32_t)2345);
	TEST_ASSERT_EQUAL(
		idx, 235, "lower_bound(2345) should return 235 (element 2350)"
	);

	BTREE_FREE(&tree);
	free(data);
	free(raw_mem);

	LOG(INFO, "✓ Large dataset test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Duplicate values
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_duplicates() {
	LOG(INFO, "Test: btree with duplicate values");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// lower_bound should return first occurrence
	size_t idx = BTREE_LOWER_BOUND(&tree, 5);
	TEST_ASSERT(
		idx >= 1 && idx <= 3,
		"lower_bound(5) should return index of first 5"
	);

	idx = BTREE_LOWER_BOUND(&tree, 20);
	TEST_ASSERT(
		idx >= 7 && idx <= 10,
		"lower_bound(20) should return index of first 20"
	);

	// upper_bound should return past last occurrence
	idx = BTREE_UPPER_BOUND(&tree, 5);
	TEST_ASSERT(idx >= 4, "upper_bound(5) should return index past last 5");

	idx = BTREE_UPPER_BOUND(&tree, 20);
	TEST_ASSERT(
		idx >= 11, "upper_bound(20) should return index past last 20"
	);

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Duplicate values test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Sequential searches
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_sequential_searches() {
	LOG(INFO, "Test: sequential searches");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Perform multiple searches to test consistency
	for (int i = 0; i < 3; i++) {
		size_t idx = BTREE_LOWER_BOUND(&tree, 45);
		TEST_ASSERT_EQUAL(
			idx, 4, "lower_bound(45) should consistently return 4"
		);

		idx = BTREE_UPPER_BOUND(&tree, 70);
		TEST_ASSERT_EQUAL(
			idx, 7, "upper_bound(70) should consistently return 7"
		);
	}

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Sequential searches test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Boundary values
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_boundary_values() {
	LOG(INFO, "Test: boundary values");

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

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Test minimum value
	size_t idx = BTREE_LOWER_BOUND(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	// Test maximum value
	idx = BTREE_LOWER_BOUND(&tree, 9);
	TEST_ASSERT_EQUAL(idx, 9, "lower_bound(9) should return 9");

	idx = BTREE_UPPER_BOUND(&tree, 9);
	TEST_ASSERT_EQUAL(idx, n, "upper_bound(9) should return n");

	BTREE_FREE(&tree);

	free(raw_mem);
	LOG(INFO, "✓ Boundary values test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Power of 2 sizes
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_power_of_2_sizes() {
	LOG(INFO, "Test: power of 2 sizes");

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

	struct btree tree16;
	int ret = BTREE_INIT(&tree16, data16, 16, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree initialization with 16 elements failed"
	);

	size_t idx = BTREE_LOWER_BOUND(&tree16, 37);
	TEST_ASSERT_EQUAL(
		idx, 8, "lower_bound(37) should return 8 (element 40)"
	);

	BTREE_FREE(&tree16);

	// Test with 64 elements (power of 2)
	uint32_t data64[64];
	for (size_t i = 0; i < 64; i++) {
		data64[i] = i * 2;
	}

	struct btree tree64;
	ret = BTREE_INIT(&tree64, data64, 64, &mctx);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree initialization with 64 elements failed"
	);

	idx = BTREE_LOWER_BOUND(&tree64, 77);
	TEST_ASSERT_EQUAL(
		idx, 39, "lower_bound(77) should return 39 (element 78)"
	);

	for (uint32_t i = 0; i < 64; ++i) {
		idx = BTREE_LOWER_BOUND(&tree64, i * 2);
		TEST_ASSERT_EQUAL(
			idx,
			i,
			"lower_bound(%d) should return %d (element %d)",
			(uint32_t)(i * 2),
			i,
			i * 2
		);

		idx = BTREE_UPPER_BOUND(&tree64, i * 2);
		TEST_ASSERT_EQUAL(
			idx,
			i + 1,
			"upper_bound(%d) should return %d (element %d)",
			(uint32_t)(i * 2),
			i + 1,
			i * 2
		);
	}

	BTREE_FREE(&tree64);

	free(raw_mem);
	LOG(INFO, "✓ Power of 2 sizes test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: some N
////////////////////////////////////////////////////////////////////////////////

static int
test_btree32_some_n(size_t n) {
	LOG(INFO, "Test: btree32_some_n(%zu)", n);

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
	uint32_t *data = malloc(n * sizeof(uint32_t));
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 2;
	}
	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT(
		ret == 0, "btree initialization with %zu elements failed", n
	);

	for (uint32_t i = 0; i < 2 * n; i++) {
		int idx = BTREE_LOWER_BOUND(&tree, i);
		TEST_ASSERT(
			idx == (int)(i + 1) / 2,
			"lower_bound(%d) should return %d (element %d)",
			(uint32_t)i,
			(i + 1) / 2,
			((i + 1) / 2) * 2
		);
	}

	for (uint32_t i = 0; i < 2 * n; ++i) {
		int idx = BTREE_UPPER_BOUND(&tree, i);
		TEST_ASSERT(
			idx == (int)i / 2 + 1,
			"upper_bound(%d) should return %d (element %d)",
			(uint32_t)i,
			i / 2 + 1,
			(i / 2 + 1) * 2
		);
	}

	BTREE_FREE(&tree);

	free(data);
	free(raw_mem);

	LOG(INFO, "✓ btree32_some_n(%zu) values test passed", n);

	return TEST_SUCCESS;
}

static int
test_btree64_some_n(size_t n) {
	LOG(INFO, "Test: btree64_some_n(%zu)", n);

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
	uint64_t *data = malloc(n * sizeof(uint64_t));
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 2;
	}
	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, &mctx);
	TEST_ASSERT(
		ret == 0, "btree initialization with %zu elements failed", n
	);

	for (uint64_t i = 0; i < 2 * n; i++) {
		int idx = BTREE_LOWER_BOUND(&tree, i);
		TEST_ASSERT(
			idx == (int)(i + 1) / 2,
			"lower_bound(%lu) should return %lu (element %lu)",
			i,
			(i + 1) / 2,
			((i + 1) / 2) * 2
		);
	}

	for (uint64_t i = 0; i < 2 * n; ++i) {
		int idx = BTREE_UPPER_BOUND(&tree, i);
		TEST_ASSERT(
			idx == (int)i / 2 + 1,
			"upper_bound(%lu) should return %lu (element %lu)",
			i,
			i / 2 + 1,
			(i / 2 + 1) * 2
		);
	}

	BTREE_FREE(&tree);

	free(data);
	free(raw_mem);

	LOG(INFO, "✓ btree64_some_n(%zu) values test passed", n);

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Main test runner
////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("info");

	LOG(INFO, "=== Starting btree test suite ===\n");

	int failed = 0;

	// Run all tests
	if (test_btree_init_free() != TEST_SUCCESS)
		failed++;
	if (test_btree_empty() != TEST_SUCCESS)
		failed++;
	if (test_btree_single_element() != TEST_SUCCESS)
		failed++;
	if (test_btree_lower_bound_uint32() != TEST_SUCCESS)
		failed++;
	if (test_btree_upper_bound_uint32() != TEST_SUCCESS)
		failed++;
	if (test_btree_uint16() != TEST_SUCCESS)
		failed++;
	if (test_btree_uint64() != TEST_SUCCESS)
		failed++;
	if (test_btree_large_dataset() != TEST_SUCCESS)
		failed++;
	if (test_btree_duplicates() != TEST_SUCCESS)
		failed++;
	if (test_btree_sequential_searches() != TEST_SUCCESS)
		failed++;
	if (test_btree_boundary_values() != TEST_SUCCESS)
		failed++;
	if (test_btree_power_of_2_sizes() != TEST_SUCCESS)
		failed++;

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
		if (test_btree32_some_n(ns[i]) != TEST_SUCCESS)
			failed++;
		if (test_btree64_some_n(ns[i]) != TEST_SUCCESS)
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