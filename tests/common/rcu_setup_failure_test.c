/* Verifies that RCU scenarios stop after real worker-allocation failure.
 *
 * Each owner is included to exercise its private setups and arena lifetime
 * without changing the production initializer.
 */

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/rcu.h"
#include "lib/logging/log.h"
#include "tests/common/helpers.h"

#include <assert.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/time.h>
#include <unistd.h>

static size_t init_attempts;
static size_t init_failures;
static size_t init_failure_at;
static size_t arena_allocations;
static size_t arena_frees;
static uintptr_t allocated_arena;
static uintptr_t freed_arena;
static uintptr_t poisoned_storage;

/* Uses an empty allocator so failure leaves the caller's storage untouched. */
static int
fail_worker_allocation(
	rcu_t *rcu, struct memory_context *mctx, size_t worker_count
) {
	init_attempts++;
	if (init_failure_at != 0 && init_attempts != init_failure_at) {
		return rcu_init(rcu, mctx, worker_count);
	}
	struct block_allocator allocator;
	struct memory_context empty_context;
	TEST_ASSERT(
		block_allocator_init(&allocator) == 0,
		"empty allocator initialization failed"
	);
	TEST_ASSERT(
		memory_context_init(&empty_context, "empty", &allocator) == 0,
		"empty context initialization failed"
	);

	asan_unpoison_memory_region(rcu, sizeof(*rcu));
	memset(rcu, 0xa5, sizeof(*rcu));
	int result = rcu_init(rcu, &empty_context, worker_count);
	memory_context_fini(&empty_context);
	if (result != 0) {
		init_failures++;
		poisoned_storage = (uintptr_t)rcu;
		asan_poison_memory_region(rcu, sizeof(*rcu));
	}
	return result;
}

/* Tracks arena ownership while retaining the real libc allocation. */
static void *
record_arena_allocation(size_t size) {
	void *arena = malloc(size);
	arena_allocations++;
	allocated_arena = (uintptr_t)arena;
	return arena;
}

/* Saves the arena identity before its lifetime ends. */
static void
record_arena_free(void *arena) {
	arena_frees++;
	freed_arena = (uintptr_t)arena;
	free(arena);
}

#define rcu_init fail_worker_allocation
#define malloc record_arena_allocation
#define free record_arena_free
#define main rcu_owner_main
#ifdef RCU_SETUP_FAILURE_BENCH
#include "rcu_bench.c"
#else
#include "rcu_test.c"
#endif
#undef main
#undef free
#undef malloc
#undef rcu_init

/* Clears shadow poisoning after the scenario's stack frame has returned. */
static int
check_failed_setups(size_t expected, size_t failures) {
	if (poisoned_storage != 0) {
		asan_unpoison_memory_region(
			(void *)poisoned_storage, sizeof(rcu_t)
		);
		poisoned_storage = 0;
	}
	TEST_ASSERT_EQUAL(
		init_attempts,
		expected,
		"every setup must attempt initialization"
	);
	TEST_ASSERT_EQUAL(
		init_failures, failures, "unexpected allocation failure count"
	);
	return TEST_SUCCESS;
}

#ifdef RCU_SETUP_FAILURE_BENCH
static int
run_scenarios_stop_after_init_failure_test(void) {
	benchmark_multiworker_throughput(1);
	TEST_ASSERT(
		check_failed_setups(1, 1) == TEST_SUCCESS,
		"throughput setup must stop after allocation failure"
	);
	benchmark_contention();
	TEST_ASSERT(
		check_failed_setups(2, 2) == TEST_SUCCESS,
		"contention setup must stop after allocation failure"
	);
	benchmark_latency_distribution();
	TEST_ASSERT(
		check_failed_setups(3, 3) == TEST_SUCCESS,
		"latency setup must stop after allocation failure"
	);
	benchmark_reader_writer_interaction(1);
	TEST_ASSERT(
		check_failed_setups(4, 4) == TEST_SUCCESS,
		"reader/writer setup must stop after allocation failure"
	);
	return TEST_SUCCESS;
}
#else
static int
run_scenarios_stop_after_init_failure_test(void) {
	const struct {
		const char *name;
		int (*run)(void);
	} scenarios[] = {
		{"basic initialization", test_basic_init},
		{"single reader", test_single_reader},
		{"single writer", test_single_writer},
		{"multiple updates", test_multiple_updates},
		{"reader/writer interaction", test_reader_writer_interaction},
		{"multiple workers", test_multiple_workers},
		{"epoch synchronization", test_epoch_synchronization},
		{"concurrent readers", test_concurrent_readers},
		{"concurrent readers with writer",
		 test_concurrent_readers_with_writer},
		{"rapid updates", test_rapid_updates},
		{"all workers active", test_all_workers_active},
		{"memory ordering", test_memory_ordering},
		{"writer load", test_rcu_load},
		{"aggressive race detection", test_aggressive_race_detection},
	};
	for (size_t i = 0; i < sizeof(scenarios) / sizeof(scenarios[0]); i++) {
		int result = scenarios[i].run();
		TEST_ASSERT_EQUAL(
			result,
			TEST_FAILED,
			"%s must report setup failure",
			scenarios[i].name
		);
		TEST_ASSERT(
			check_failed_setups(i + 1, i + 1) == TEST_SUCCESS,
			"%s must stop after allocation failure",
			scenarios[i].name
		);
	}
	return TEST_SUCCESS;
}
#endif

static int
run_entrypoint_reports_failure_and_frees_arena_test(
	size_t fail_at, size_t expected_attempts, size_t expected_failures
) {
	init_attempts = 0;
	init_failures = 0;
	init_failure_at = fail_at;
	arena_allocations = 0;
	arena_frees = 0;
	allocated_arena = 0;
	freed_arena = 0;
	int result = rcu_owner_main();
	int setup_result =
		check_failed_setups(expected_attempts, expected_failures);
#ifdef RCU_SETUP_FAILURE_BENCH
	const int expected_result = 1;
#else
	const int expected_result = TEST_FAILED;
#endif
	TEST_ASSERT_EQUAL(
		result, expected_result, "owner must report setup failure"
	);
	TEST_ASSERT(
		setup_result == TEST_SUCCESS,
		"owner entrypoint must handle failed setups"
	);
	TEST_ASSERT_EQUAL(
		arena_allocations, 1, "owner must allocate one arena"
	);
	TEST_ASSERT(
		allocated_arena != 0, "owner arena allocation must succeed"
	);
	TEST_ASSERT_EQUAL(arena_frees, 1, "owner must release its arena once");
	TEST_ASSERT_EQUAL(
		freed_arena,
		allocated_arena,
		"owner must release its allocated arena"
	);
	return TEST_SUCCESS;
}

int
main(void) {
	if (run_scenarios_stop_after_init_failure_test() != TEST_SUCCESS) {
		return TEST_FAILED;
	}
#ifdef RCU_SETUP_FAILURE_BENCH
	const size_t failures[] = {1, 2, 5, 6, 7};
	for (size_t i = 0; i < sizeof(failures) / sizeof(failures[0]); i++) {
		TEST_ASSERT(
			run_entrypoint_reports_failure_and_frees_arena_test(
				failures[i], failures[i], 1
			) == TEST_SUCCESS,
			"benchmark must propagate initialization failure %zu",
			failures[i]
		);
	}
	return TEST_SUCCESS;
#else
	return run_entrypoint_reports_failure_and_frees_arena_test(0, 14, 14);
#endif
}
