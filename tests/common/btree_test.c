#include "common/btree.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////
// Test: Basic initialization and cleanup
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_init_free() {
	LOG(INFO, "Test: btree init and free");

	uint32_t data[] = {1, 5, 10, 15, 20, 25, 30};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Verify the tree was initialized
	TEST_ASSERT(tree.array.size > 0, "btree array size should be > 0");

	BTREE_FREE(&tree);

	// Verify cleanup (array should be zeroed)
	TEST_ASSERT_EQUAL(tree.array.size, 0, "btree not properly freed");

	LOG(INFO, "✓ Basic init/free test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Empty tree
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_empty() {
	LOG(INFO, "Test: empty btree");

	uint32_t *data = NULL;
	size_t n = 0;

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
	TEST_ASSERT_EQUAL(ret, 0, "empty btree initialization failed");

	BTREE_FREE(&tree);

	LOG(INFO, "✓ Empty tree test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Single element
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_single_element() {
	LOG(INFO, "Test: single element btree");

	uint32_t data[] = {42};
	size_t n = 1;

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
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

	LOG(INFO, "✓ Single element test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: LOWER_BOUND with uint32_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_lower_bound_uint32() {
	LOG(INFO, "Test: BTREE_LOWER_BOUND with uint32_t");

	uint32_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Test exact matches
	size_t idx = BTREE_LOWER_BOUND(&tree, 1);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(1) should return 0");

	idx = BTREE_LOWER_BOUND(&tree, 15);
	TEST_ASSERT_EQUAL(idx, 3, "lower_bound(15) should return 3");

	idx = BTREE_LOWER_BOUND(&tree, 40);
	TEST_ASSERT_EQUAL(idx, 8, "lower_bound(40) should return 8");

	// Test values between elements
	idx = BTREE_LOWER_BOUND(&tree, 12);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(12) should return 3 (element 15)"
	);

	idx = BTREE_LOWER_BOUND(&tree, 27);
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

	LOG(INFO, "✓ LOWER_BOUND uint32_t test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: UPPER_BOUND with uint32_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_upper_bound_uint32() {
	LOG(INFO, "Test: BTREE_UPPER_BOUND with uint32_t");

	uint32_t data[] = {1, 5, 10, 15, 20, 25, 30, 35, 40};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
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

	LOG(INFO, "✓ UPPER_BOUND uint32_t test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Different data types - uint8_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_uint8() {
	LOG(INFO, "Test: btree with uint8_t");

	uint8_t data[] = {10, 20, 30, 40, 50, 60, 70, 80, 90, 100};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	size_t idx = BTREE_LOWER_BOUND(&tree, 45);
	TEST_ASSERT_EQUAL(
		idx, 4, "lower_bound(45) should return 4 (element 50)"
	);

	idx = BTREE_UPPER_BOUND(&tree, 60);
	TEST_ASSERT_EQUAL(
		idx, 6, "upper_bound(60) should return 6 (element 70)"
	);

	BTREE_FREE(&tree);

	LOG(INFO, "✓ uint8_t test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Different data types - uint16_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_uint16() {
	LOG(INFO, "Test: btree with uint16_t");

	uint16_t data[] = {100, 200, 300, 400, 500, 600, 700, 800};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	size_t idx = BTREE_LOWER_BOUND(&tree, 350);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(350) should return 3 (element 400)"
	);

	idx = BTREE_UPPER_BOUND(&tree, 500);
	TEST_ASSERT_EQUAL(
		idx, 5, "upper_bound(500) should return 5 (element 600)"
	);

	BTREE_FREE(&tree);

	LOG(INFO, "✓ uint16_t test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Different data types - uint64_t
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_uint64() {
	LOG(INFO, "Test: btree with uint64_t");

	uint64_t data[] = {1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	size_t idx = BTREE_LOWER_BOUND(&tree, 3500);
	TEST_ASSERT_EQUAL(
		idx, 3, "lower_bound(3500) should return 3 (element 4000)"
	);

	idx = BTREE_UPPER_BOUND(&tree, 5000);
	TEST_ASSERT_EQUAL(
		idx, 5, "upper_bound(5000) should return 5 (element 6000)"
	);

	BTREE_FREE(&tree);

	LOG(INFO, "✓ uint64_t test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Large dataset
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_large_dataset() {
	LOG(INFO, "Test: btree with large dataset");

	const size_t n = 1000;
	uint32_t *data = malloc(n * sizeof(uint32_t));
	TEST_ASSERT_NOT_NULL(data, "failed to allocate test data");

	// Create sorted array: 0, 10, 20, 30, ...
	for (size_t i = 0; i < n; i++) {
		data[i] = i * 10;
	}

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
	TEST_ASSERT_EQUAL(ret, 0, "btree initialization failed");

	// Test various searches
	size_t idx = BTREE_LOWER_BOUND(&tree, 0);
	TEST_ASSERT_EQUAL(idx, 0, "lower_bound(0) should return 0");

	idx = BTREE_LOWER_BOUND(&tree, 5000);
	TEST_ASSERT_EQUAL(idx, 500, "lower_bound(5000) should return 500");

	idx = BTREE_LOWER_BOUND(&tree, 9995);
	TEST_ASSERT_EQUAL(idx, n, "lower_bound(9995) should return n");

	idx = BTREE_UPPER_BOUND(&tree, 4990);
	TEST_ASSERT_EQUAL(idx, 500, "upper_bound(4990) should return 500");

	// Test search for values between elements
	idx = BTREE_LOWER_BOUND(&tree, 2345);
	TEST_ASSERT_EQUAL(
		idx, 235, "lower_bound(2345) should return 235 (element 2350)"
	);

	BTREE_FREE(&tree);
	free(data);

	LOG(INFO, "✓ Large dataset test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Duplicate values
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_duplicates() {
	LOG(INFO, "Test: btree with duplicate values");

	uint32_t data[] = {1, 5, 5, 5, 10, 10, 15, 20, 20, 20, 20, 25};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
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

	LOG(INFO, "✓ Duplicate values test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Sequential searches
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_sequential_searches() {
	LOG(INFO, "Test: sequential searches");

	uint32_t data[] = {10, 20, 30, 40, 50, 60, 70, 80, 90, 100};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
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

	LOG(INFO, "✓ Sequential searches test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Boundary values
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_boundary_values() {
	LOG(INFO, "Test: boundary values");

	uint32_t data[] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9};
	size_t n = sizeof(data) / sizeof(data[0]);

	struct btree tree;
	int ret = BTREE_INIT(&tree, data, n, NULL);
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

	LOG(INFO, "✓ Boundary values test passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test: Power of 2 sizes
////////////////////////////////////////////////////////////////////////////////

static int
test_btree_power_of_2_sizes() {
	LOG(INFO, "Test: power of 2 sizes");

	// Test with 16 elements (power of 2)
	uint32_t data16[16];
	for (size_t i = 0; i < 16; i++) {
		data16[i] = i * 5;
	}

	struct btree tree16;
	int ret = BTREE_INIT(&tree16, data16, 16, NULL);
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
	ret = BTREE_INIT(&tree64, data64, 64, NULL);
	TEST_ASSERT_EQUAL(
		ret, 0, "btree initialization with 64 elements failed"
	);

	idx = BTREE_LOWER_BOUND(&tree64, 77);
	TEST_ASSERT_EQUAL(
		idx, 39, "lower_bound(77) should return 39 (element 78)"
	);

	BTREE_FREE(&tree64);

	LOG(INFO, "✓ Power of 2 sizes test passed");
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
	if (test_btree_uint8() != TEST_SUCCESS)
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

	if (failed == 0) {
		LOG(INFO, "\n=== All btree tests passed! ===");
		return 0;
	} else {
		LOG(ERROR, "\n=== %d test(s) failed ===", failed);
		return 1;
	}
}