/* System headers */
#include <assert.h>
#include <stdlib.h>

/* Project headers */
#include "common/memory.h"
#include <lib/logging/log.h>

#include "../../dataplane/ring.h"
#include "common/memory_address.h"
#include "modules/pdump/tests/helpers.h"

#define ARENA_SIZE (1 << 20)

static void
fill_real(struct real *real, size_t idx, uint16_t weight) {
	memset(real, 0, sizeof(struct real));
	real->registry_idx = idx;
	real->weight = weight;
}

static int
test_ring_basic_usage(struct memory_context *mctx) {
	struct ring ring;
	struct real reals[100];
	for (size_t i = 0; i < 100; ++i) {
		fill_real(&reals[i], i * 2, 10 * i);
	}
	size_t counts[100 * 2];
	memset(counts, 0, sizeof(counts));
	int res = ring_init(&ring, mctx, 100, reals);
	TEST_ASSERT_EQUAL(res, 0, "failed to init ring");

	uint64_t *ids = ADDR_OF(&ring.ids);
	for (size_t i = 0; i < ring.len; ++i) {
		++counts[ids[i]];
	}

	for (size_t i = 0; i < 2 * 100; ++i) {
		size_t c = counts[i];
		if (i % 2 == 1) {
			TEST_ASSERT_EQUAL(
				c, 0, "no odd indices should be in ring"
			);
		} else {
			TEST_ASSERT_EQUAL(
				c, 10 * (i / 2), "bad count for even index"
			);
		}
	}

	ring_free(&ring);
	return TEST_SUCCESS;
}

// static int
// test_ring_weighted_distribution(struct memory_context *memory_context) {
// 	struct ring ring;
// 	ring_init(&ring, memory_context, 2);

// 	ring_change_weight(&ring, 0, 100);
// 	ring_change_weight(&ring, 1, 300); // Real 1 should get 3x more requests

// 	// Sample the distribution
// 	uint32_t count[2] = {0};
// 	for (uint32_t i = 0; i < 40000; i++) {
// 		uint32_t id = ring_get(&ring, i);
// 		TEST_ASSERT(id < 2, "valid real id returned");
// 		count[id]++;
// 	}

// 	// real 1 should get approximately 3x the requests of real 0
// 	double ratio = (double)count[1] / (double)count[0];
// 	TEST_ASSERT(
// 		ratio > 2.8 && ratio < 3.2, "weight ratio is respected (3:1)"
// 	);

// 	ring_free(&ring);
// 	return TEST_SUCCESS;
// }

// static int
// test_ring_single_real(struct memory_context *memory_context) {
// 	struct ring ring;
// 	ring_init(&ring, memory_context, 1);

// 	ring_change_weight(&ring, 0, 50);

// 	// All requests should go to real 0
// 	for (int i = 0; i < 100; i++) {
// 		uint32_t id = ring_get(&ring, i);
// 		TEST_ASSERT_EQUAL(0, id, "single real always selected");
// 	}

// 	ring_free(&ring);
// 	return TEST_SUCCESS;
// }

// static int
// test_ring_disable(struct memory_context *memory_context) {
// 	struct ring ring;
// 	ring_init(&ring, memory_context, 3);

// 	ring_change_weight(&ring, 0, 10);
// 	ring_change_weight(&ring, 1, 10);
// 	ring_change_weight(&ring, 2, 10);

// 	ring_change_weight(&ring, 1, 0);

// 	// Real 1 should never be selected
// 	for (int i = 0; i < 1000; i++) {
// 		uint32_t id = ring_get(&ring, i);
// 		TEST_ASSERT(id != 1, "disabled real never selected");
// 	}

// 	ring_free(&ring);
// 	return TEST_SUCCESS;
// }

// static int
// test_ring_all_zero_except_one(struct memory_context *memory_context) {
// 	struct ring ring;
// 	ring_init(&ring, memory_context, 3);

// 	ring_change_weight(&ring, 2, 1);

// 	// Only real 2 should be selected
// 	for (int i = 0; i < 100; i++) {
// 		uint32_t id = ring_get(&ring, i);
// 		TEST_ASSERT_EQUAL(2, id, "only enabled real selected");
// 	}

// 	ring_free(&ring);
// 	return TEST_SUCCESS;
// }

// static int
// test_ring_rapid_weight_changes(struct memory_context *memory_context) {
// 	struct ring ring;
// 	ring_init(&ring, memory_context, 4);

// 	// Perform many weight changes
// 	for (int i = 0; i < 50; i++) {
// 		uint32_t real = i % 4;
// 		uint32_t new_weight = (i * 7) % 100 + 1; // Pseudo-random weight

// 		int ret = ring_change_weight(&ring, real, new_weight);
// 		TEST_ASSERT_EQUAL(0, ret, "weight change succeeds");

// 		// Verify we can still get valid results
// 		for (int j = 0; j < 10; j++) {
// 			uint32_t id = ring_get(&ring, j + i * 1000);
// 			TEST_ASSERT(id < 4, "valid id after weight change");
// 		}
// 	}

// 	ring_free(&ring);
// 	return TEST_SUCCESS;
// }

// static int
// test_ring_zero_reals(struct memory_context *memory_context) {
// 	struct ring ring;
// 	int ret = ring_init(&ring, memory_context, 0);

// 	TEST_ASSERT_EQUAL(0, ret, "ring_init succeeds with zero reals");

// 	// Getting from empty ring should return RING_VALUE_INVALID
// 	uint32_t id = ring_get(&ring, 0);
// 	TEST_ASSERT_EQUAL(
// 		RING_VALUE_INVALID,
// 		id,
// 		"ring_get returns RING_VALUE_INVALID for empty ring"
// 	);

// 	// Try multiple values to ensure consistent behavior
// 	id = ring_get(&ring, 12345);
// 	TEST_ASSERT_EQUAL(
// 		RING_VALUE_INVALID, id, "ring_get consistently returns invalid"
// 	);

// 	id = ring_get(&ring, UINT64_MAX);
// 	TEST_ASSERT_EQUAL(
// 		RING_VALUE_INVALID, id, "ring_get returns invalid for any input"
// 	);

// 	ring_free(&ring);
// 	return TEST_SUCCESS;
// }

// static int
// test_ring_all_become_disabled(struct memory_context *memory_context) {
// 	struct ring ring;
// 	ring_init(&ring, memory_context, 3);

// 	ring_change_weight(&ring, 0, 10);
// 	ring_change_weight(&ring, 1, 10);
// 	ring_change_weight(&ring, 2, 10);

// 	// Set all weights to zero one by one
// 	ring_change_weight(&ring, 0, 0);
// 	ring_change_weight(&ring, 1, 0);
// 	ring_change_weight(&ring, 2, 0);

// 	uint32_t id = ring_get(&ring, 300);
// 	TEST_ASSERT_EQUAL(
// 		RING_VALUE_INVALID,
// 		id,
// 		"ring_get returns invalid when all disabled"
// 	);

// 	// Re-enable one real
// 	ring_change_weight(&ring, 0, 5);

// 	// real 0 should now be the only one selected
// 	for (int i = 0; i < 50; i++) {
// 		id = ring_get(&ring, i);
// 		TEST_ASSERT_EQUAL(0, id, "re-enabled real is selected");
// 	}

// 	ring_free(&ring);
// 	return TEST_SUCCESS;
// }

/* Test suite structure */
struct test_case {
	const char *name;
	int (*test_func)(struct memory_context *);
};

static struct test_case test_cases[] = {
	{"basic usage", test_ring_basic_usage},
	// {"weighted distribution", test_ring_weighted_distribution},
	// {"single real", test_ring_single_real},
	// {"disable real", test_ring_disable},
	// {"all zero except one", test_ring_all_zero_except_one},
	// {"rapid weight changes", test_ring_rapid_weight_changes},
	// {"zero reals", test_ring_zero_reals},
	// {"all become disabled", test_ring_all_become_disabled},
};

int
main() {
	log_enable_name("debug");

	int failed_tests = 0;
	int total_tests = sizeof(test_cases) / sizeof(test_cases[0]);

	LOG(INFO, "Starting ring unit tests...");
	LOG(INFO, "Running %d test cases", total_tests);

	for (int i = 0; i < total_tests; i++) {
		LOG(INFO,
		    "Running test %d/%d: %s",
		    i + 1,
		    total_tests,
		    test_cases[i].name);

		void *arena = malloc(ARENA_SIZE);
		if (arena == NULL) {
			return TEST_FAILED;
		}

		struct block_allocator alloc;
		block_allocator_init(&alloc);
		block_allocator_put_arena(&alloc, arena, ARENA_SIZE);

		struct memory_context memory_context;
		if (memory_context_init(&memory_context, "test", &alloc) < 0) {
			return TEST_FAILED;
		}

		int result = test_cases[i].test_func(&memory_context);
		if (result == TEST_SUCCESS) {
			LOG(INFO, "✓ PASSED: %s", test_cases[i].name);
			if (memory_context.bfree_size !=
			    memory_context.balloc_size) {
				LOG(ERROR,
				    "✗ FAILED: %s: memory leak",
				    test_cases[i].name);
				failed_tests++;
			}
		} else {
			LOG(ERROR, "✗ FAILED: %s", test_cases[i].name);
			failed_tests++;
		}
		free(arena);
	}

	LOG(INFO,
	    "Test summary: %d/%d tests passed, %d failed",
	    total_tests - failed_tests,
	    total_tests,
	    failed_tests);

	if (failed_tests == 0) {
		LOG(INFO,
		    "All tests passed! Ring implementation is "
		    "working correctly.");
		return 0;
	} else {
		LOG(ERROR,
		    "Some tests failed. Please review the implementation.");
		return 1;
	}
}
