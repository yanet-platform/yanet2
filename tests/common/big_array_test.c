#include "common/big_array.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"
#include "lib/logging/log.h"

#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////
// Helper functions
////////////////////////////////////////////////////////////////////////////////

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

////////////////////////////////////////////////////////////////////////////////
// Test cases
////////////////////////////////////////////////////////////////////////////////

/**
 * Test basic initialization and cleanup of a small array
 * (smaller than MEMORY_BLOCK_ALLOCATOR_MAX_SIZE)
 */
static int
test_init_and_free_basic(void) {
	LOG(INFO, "test_init_and_free_basic");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_test", &ba) == 0,
		"memory_context_init failed"
	);

	// Initialize a small array (1000 bytes)
	struct big_array array;
	const size_t array_size = 1000;
	int res = big_array_init(&array, array_size, &mctx);
	TEST_ASSERT(res == 0, "big_array_init failed");

	// Verify array was initialized
	TEST_ASSERT(array.subarrays != NULL, "subarrays pointer is NULL");
	TEST_ASSERT(array.subarrays_count > 0, "subarrays_count should be > 0");

	// For small arrays, we expect exactly 1 subarray
	TEST_ASSERT(
		array.subarrays_count == 1,
		"expected 1 subarray for small array, got %zu",
		array.subarrays_count
	);

	// Verify memory context tracking in child context
	TEST_ASSERT(
		array.mctx.balloc_count > 0,
		"child context balloc_count should be incremented"
	);

	// Free the array
	big_array_free(&array);

	// Verify cleanup
	TEST_ASSERT(
		array.subarrays == NULL, "subarrays should be NULL after free"
	);
	TEST_ASSERT(
		array.subarrays_count == 0,
		"subarrays_count should be 0 after free"
	);

	free(raw_mem);
	return TEST_SUCCESS;
}

/**
 * Test initialization of a large array that requires multiple subarrays
 * (larger than MEMORY_BLOCK_ALLOCATOR_MAX_SIZE)
 */
static int
test_init_large_array(void) {
	LOG(INFO, "test_init_large_array");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 29; // 512 MiB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_large", &ba) == 0,
		"memory_context_init failed"
	);

	// Initialize array larger than MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
	// MEMORY_BLOCK_ALLOCATOR_MAX_SIZE is typically 64MB (- 128 bytes with
	// ASAN)
	const size_t array_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE * 3 - 1000;
	struct big_array array;
	int res = big_array_init(&array, array_size, &mctx);
	TEST_ASSERT(res == 0, "big_array_init failed for large array");

	// Verify multiple subarrays were created
	TEST_ASSERT(
		array.subarrays_count > 1,
		"expected multiple subarrays, got %zu",
		array.subarrays_count
	);

	// Calculate expected subarray count
	size_t subarray_size = 1ULL << array.subarray_len_exp;
	size_t expected_count =
		(array_size + subarray_size - 1) / subarray_size;
	TEST_ASSERT(
		array.subarrays_count == expected_count,
		"expected %zu subarrays, got %zu",
		expected_count,
		array.subarrays_count
	);

	LOG(INFO,
	    "Large array: size=%zu, subarrays=%zu, subarray_size=%zu",
	    array_size,
	    array.subarrays_count,
	    subarray_size);

	big_array_free(&array);
	free(raw_mem);
	return TEST_SUCCESS;
}

/**
 * Test initialization with size exactly at subarray boundary
 */
static int
test_init_exact_boundary(void) {
	LOG(INFO, "test_init_exact_boundary");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 29; // 512 MiB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_boundary", &ba) == 0,
		"memory_context_init failed"
	);

	// Initialize with size exactly at boundary
	const size_t array_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE * 2 + 1505;
	struct big_array array;
	int res = big_array_init(&array, array_size, &mctx);
	TEST_ASSERT(res == 0, "big_array_init failed at boundary");

	// Verify correct subarray count (using ceiling division)
	size_t subarray_size = 1ULL << array.subarray_len_exp;
	size_t expected_count =
		(array_size + subarray_size - 1) / subarray_size;
	TEST_ASSERT(
		array.subarrays_count == expected_count,
		"expected %zu subarrays at boundary, got %zu",
		expected_count,
		array.subarrays_count
	);

	big_array_free(&array);
	free(raw_mem);
	return TEST_SUCCESS;
}

/**
 * Test initialization with zero size
 */
static int
test_init_zero_size(void) {
	LOG(INFO, "test_init_zero_size");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_zero", &ba) == 0,
		"memory_context_init failed"
	);

	// Initialize with size 0
	struct big_array array;
	int res = big_array_init(&array, 0, &mctx);
	TEST_ASSERT(res == 0, "big_array_init failed with size 0");

	// Verify array state
	TEST_ASSERT(
		array.subarrays_count == 0,
		"expected 0 subarrays for size 0, got %zu",
		array.subarrays_count
	);

	// Free should be safe even with zero size
	big_array_free(&array);

	free(raw_mem);
	return TEST_SUCCESS;
}

/**
 * Test element access patterns
 */
static int
test_get_access_patterns(void) {
	LOG(INFO, "test_get_access_patterns");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_access", &ba) == 0,
		"memory_context_init failed"
	);

	// Initialize array with known size
	const size_t array_size = 10000;
	struct big_array array;
	int res = big_array_init(&array, array_size, &mctx);
	TEST_ASSERT(res == 0, "big_array_init failed");

	// Access first element
	uint8_t *first = (uint8_t *)big_array_get(&array, 0);
	TEST_ASSERT(first != NULL, "first element access returned NULL");
	*first = 0xAA;
	TEST_ASSERT(*first == 0xAA, "failed to write to first element");

	// Access last element
	uint8_t *last = (uint8_t *)big_array_get(&array, array_size - 1);
	TEST_ASSERT(last != NULL, "last element access returned NULL");
	*last = 0xBB;
	TEST_ASSERT(*last == 0xBB, "failed to write to last element");

	// Access middle element
	uint8_t *middle = (uint8_t *)big_array_get(&array, array_size / 2);
	TEST_ASSERT(middle != NULL, "middle element access returned NULL");
	*middle = 0xCC;
	TEST_ASSERT(*middle == 0xCC, "failed to write to middle element");

	// Verify pointer ordering
	TEST_ASSERT(
		first < middle && middle < last, "pointer ordering is incorrect"
	);

	// Write pattern and verify
	for (size_t i = 0; i < array_size; i++) {
		uint8_t *ptr = (uint8_t *)big_array_get(&array, i);
		*ptr = (uint8_t)(i & 0xFF);
	}

	for (size_t i = 0; i < array_size; i++) {
		uint8_t *ptr = (uint8_t *)big_array_get(&array, i);
		TEST_ASSERT(
			*ptr == (uint8_t)(i & 0xFF),
			"pattern mismatch at index %zu: expected %u, got %u",
			i,
			(uint8_t)(i & 0xFF),
			*ptr
		);
	}

	big_array_free(&array);
	free(raw_mem);
	return TEST_SUCCESS;
}

/**
 * Test access across multiple subarrays
 */
static int
test_get_multiple_subarrays(void) {
	LOG(INFO, "test_get_multiple_subarrays");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 29; // 512 MiB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_multi", &ba) == 0,
		"memory_context_init failed"
	);

	// Create array spanning multiple subarrays
	const size_t array_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE * 2 + 1000;
	struct big_array array;
	int res = big_array_init(&array, array_size, &mctx);
	TEST_ASSERT(res == 0, "big_array_init failed");

	TEST_ASSERT(
		array.subarrays_count >= 2,
		"expected at least 2 subarrays, got %zu",
		array.subarrays_count
	);

	size_t subarray_size = 1ULL << array.subarray_len_exp;

	// Access element in first subarray
	size_t idx1 = 100;
	uint8_t *ptr1 = (uint8_t *)big_array_get(&array, idx1);
	TEST_ASSERT(ptr1 != NULL, "access in first subarray failed");
	*ptr1 = 0x11;

	// Access element in second subarray
	size_t idx2 = subarray_size + 100;
	uint8_t *ptr2 = (uint8_t *)big_array_get(&array, idx2);
	TEST_ASSERT(ptr2 != NULL, "access in second subarray failed");
	*ptr2 = 0x22;

	// Access element at subarray boundary
	size_t idx3 = subarray_size - 1;
	uint8_t *ptr3 = (uint8_t *)big_array_get(&array, idx3);
	TEST_ASSERT(ptr3 != NULL, "access at boundary failed");
	*ptr3 = 0x33;

	// Verify all values
	TEST_ASSERT(*ptr1 == 0x11, "value in first subarray corrupted");
	TEST_ASSERT(*ptr2 == 0x22, "value in second subarray corrupted");
	TEST_ASSERT(*ptr3 == 0x33, "value at boundary corrupted");

	LOG(INFO,
	    "Multi-subarray test: array_size=%zu, subarrays=%zu, "
	    "subarray_size=%zu",
	    array_size,
	    array.subarrays_count,
	    subarray_size);

	big_array_free(&array);
	free(raw_mem);
	return TEST_SUCCESS;
}

/**
 * Test double free safety
 */
static int
test_double_free_safety(void) {
	LOG(INFO, "test_double_free_safety");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 26; // 64 MiB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_double_free", &ba) == 0,
		"memory_context_init failed"
	);

	// Initialize array
	struct big_array array;
	int res = big_array_init(&array, 1000, &mctx);
	TEST_ASSERT(res == 0, "big_array_init failed");

	// First free
	big_array_free(&array);

	// Second free should be safe (no crash)
	big_array_free(&array);

	// Verify array is properly zeroed
	TEST_ASSERT(
		array.subarrays == NULL, "subarrays not NULL after double free"
	);
	TEST_ASSERT(
		array.subarrays_count == 0,
		"subarrays_count not 0 after double free"
	);

	free(raw_mem);
	return TEST_SUCCESS;
}

/**
 * Test with array size bigger than MEMORY_BLOCK_ALLOCATOR_MAX_SIZE
 */
static int
test_size_bigger_than_max(void) {
	LOG(INFO, "test_size_bigger_than_max");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 30; // 1GB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_max_size", &ba) == 0,
		"memory_context_init failed"
	);

	// Create array significantly larger than max block size
	const size_t array_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE * 5 + 12345;
	struct big_array array;
	int res = big_array_init(&array, array_size, &mctx);
	TEST_ASSERT(
		res == 0,
		"big_array_init failed for size > "
		"MEMORY_BLOCK_ALLOCATOR_MAX_SIZE"
	);

	// Verify multiple subarrays
	TEST_ASSERT(
		array.subarrays_count >= 5,
		"expected at least 5 subarrays, got %zu",
		array.subarrays_count
	);

	size_t subarray_size = 1ULL << array.subarray_len_exp;
	LOG(INFO,
	    "Max size test: array_size=%zu, max_block=%zu, subarrays=%zu, "
	    "subarray_size=%zu",
	    array_size,
	    (size_t)MEMORY_BLOCK_ALLOCATOR_MAX_SIZE,
	    array.subarrays_count,
	    subarray_size);

	// Test access across all subarrays
	for (size_t i = 0; i < array.subarrays_count; i++) {
		size_t idx = i * subarray_size + 42;
		if (idx < array_size) {
			uint8_t *ptr = (uint8_t *)big_array_get(&array, idx);
			TEST_ASSERT(
				ptr != NULL,
				"access failed in subarray %zu at index %zu",
				i,
				idx
			);
			*ptr = (uint8_t)(i & 0xFF);
		}
	}

	// Verify values
	for (size_t i = 0; i < array.subarrays_count; i++) {
		size_t idx = i * subarray_size + 42;
		if (idx < array_size) {
			uint8_t *ptr = (uint8_t *)big_array_get(&array, idx);
			TEST_ASSERT(
				*ptr == (uint8_t)(i & 0xFF),
				"value mismatch in subarray %zu",
				i
			);
		}
	}

	big_array_free(&array);
	free(raw_mem);
	return TEST_SUCCESS;
}

/**
 * Test that last subarray is allocated with correct (smaller) size
 */
static int
test_last_subarray_size_optimization(void) {
	LOG(INFO, "test_last_subarray_size_optimization");

	struct block_allocator ba;
	void *raw_mem = NULL;
	const size_t arena_size = 1 << 29; // 512 MiB
	TEST_ASSERT(
		setup_allocator(&ba, &raw_mem, arena_size) == TEST_SUCCESS,
		"setup_allocator failed"
	);

	struct memory_context mctx;
	TEST_ASSERT(
		memory_context_init(&mctx, "big_array_last_size", &ba) == 0,
		"memory_context_init failed"
	);

	// Create array where last subarray should be smaller
	// e.g., 2.5 * MAX_SIZE means last subarray is 0.5 * MAX_SIZE
	const size_t array_size = MEMORY_BLOCK_ALLOCATOR_MAX_SIZE * 2 +
				  MEMORY_BLOCK_ALLOCATOR_MAX_SIZE / 2;
	struct big_array array;
	int res = big_array_init(&array, array_size, &mctx);
	TEST_ASSERT(res == 0, "big_array_init failed");

	// Verify size field is set correctly
	TEST_ASSERT(
		array.size == array_size,
		"array.size mismatch: expected %zu, got %zu",
		array_size,
		array.size
	);

	size_t balloc_size_before_free = array.mctx.balloc_size;

	// Free and verify memory accounting
	big_array_free(&array);

	// Verify all memory was freed in child context
	TEST_ASSERT(
		array.mctx.balloc_count == array.mctx.bfree_count,
		"memory leak: balloc=%zu, bfree=%zu",
		array.mctx.balloc_count,
		array.mctx.bfree_count
	);
	TEST_ASSERT(
		array.mctx.balloc_size == array.mctx.bfree_size,
		"memory leak: balloc_size=%zu, bfree_size=%zu",
		array.mctx.balloc_size,
		array.mctx.bfree_size
	);

	LOG(INFO,
	    "Last subarray optimization: total_size=%zu, allocated=%zu bytes",
	    array_size,
	    balloc_size_before_free);

	free(raw_mem);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Main test runner
////////////////////////////////////////////////////////////////////////////////

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "Starting big_array tests...");
	LOG(INFO,
	    "MEMORY_BLOCK_ALLOCATOR_MAX_SIZE = %zu",
	    (size_t)MEMORY_BLOCK_ALLOCATOR_MAX_SIZE);

	if (test_init_and_free_basic() != TEST_SUCCESS) {
		LOG(ERROR, "test_init_and_free_basic failed");
		return -1;
	}

	if (test_init_large_array() != TEST_SUCCESS) {
		LOG(ERROR, "test_init_large_array failed");
		return -1;
	}

	if (test_init_exact_boundary() != TEST_SUCCESS) {
		LOG(ERROR, "test_init_exact_boundary failed");
		return -1;
	}

	if (test_init_zero_size() != TEST_SUCCESS) {
		LOG(ERROR, "test_init_zero_size failed");
		return -1;
	}

	if (test_get_access_patterns() != TEST_SUCCESS) {
		LOG(ERROR, "test_get_access_patterns failed");
		return -1;
	}

	if (test_get_multiple_subarrays() != TEST_SUCCESS) {
		LOG(ERROR, "test_get_multiple_subarrays failed");
		return -1;
	}

	if (test_double_free_safety() != TEST_SUCCESS) {
		LOG(ERROR, "test_double_free_safety failed");
		return -1;
	}

	if (test_size_bigger_than_max() != TEST_SUCCESS) {
		LOG(ERROR, "test_size_bigger_than_max failed");
		return -1;
	}

	if (test_last_subarray_size_optimization() != TEST_SUCCESS) {
		LOG(ERROR, "test_last_subarray_size_optimization failed");
		return -1;
	}

	LOG(INFO, "All big_array tests passed!");
	return 0;
}