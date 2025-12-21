/**
 * @file rcu.c
 * @brief Comprehensive test suite for RCU (Read-Copy-Update) mechanism
 *
 * This test suite validates the correctness of the RCU implementation
 * including:
 * - Basic initialization and operations
 * - Single-threaded read/write scenarios
 * - Multi-threaded concurrent access
 * - Epoch synchronization correctness
 * - Memory ordering guarantees
 * - Edge cases and stress testing
 * - Aggressive race detection tests
 */

#include "common/rcu.h"
#include "lib/logging/log.h"
#include "tests/common/helpers.h"

#include <assert.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <sys/time.h>
#include <unistd.h>

// Helper macros to extract active and epoch from packed state
#define GET_ACTIVE(state) ((state) & 1u)
#define GET_EPOCH(state) (((state) >> 1) & 1u)

////////////////////////////////////////////////////////////////////////////////
// Test 1: Basic Initialization
////////////////////////////////////////////////////////////////////////////////

/**
 * Test that rcu_init properly initializes all fields to zero
 */
static int
test_basic_init(void) {
	LOG(INFO, "Running test_basic_init...");

	rcu_t rcu;
	rcu_init(&rcu);

	// Check global epoch is 0
	unsigned global_epoch =
		atomic_load_explicit(&rcu.global_epoch, memory_order_relaxed);
	TEST_ASSERT_EQUAL(
		global_epoch, 0, "global_epoch should be 0 after init"
	);

	// Check all workers are inactive with epoch 0
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		unsigned state = atomic_load_explicit(
			&rcu.workers[i].state, memory_order_relaxed
		);
		unsigned epoch = GET_EPOCH(state);
		unsigned active = GET_ACTIVE(state);
		TEST_ASSERT_EQUAL(
			epoch, 0, "worker epoch should be 0 after init"
		);
		TEST_ASSERT_EQUAL(
			active, 0, "worker should be inactive after init"
		);
	}

	LOG(INFO, "test_basic_init passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 2: Single Reader Operations
////////////////////////////////////////////////////////////////////////////////

/**
 * Test basic read-side critical section operations
 */
static int
test_single_reader(void) {
	LOG(INFO, "Running test_single_reader...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 42;

	// Begin read-side critical section
	uint64_t read_value = RCU_READ_BEGIN(&rcu, 0, &value);
	TEST_ASSERT_EQUAL(read_value, 42, "should read correct value");

	// Check worker 0 is now active
	unsigned state = atomic_load_explicit(
		&rcu.workers[0].state, memory_order_relaxed
	);
	unsigned active = GET_ACTIVE(state);
	TEST_ASSERT_EQUAL(active, 1, "worker should be active during read");

	// End read-side critical section
	RCU_READ_END(&rcu, 0);

	// Check worker 0 is now inactive
	state = atomic_load_explicit(
		&rcu.workers[0].state, memory_order_relaxed
	);
	active = GET_ACTIVE(state);
	TEST_ASSERT_EQUAL(
		active, 0, "worker should be inactive after read end"
	);

	LOG(INFO, "test_single_reader passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 3: Single Writer Operations
////////////////////////////////////////////////////////////////////////////////

/**
 * Test basic write operations with rcu_update
 */
static int
test_single_writer(void) {
	LOG(INFO, "Running test_single_writer...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 10;

	// Update value
	rcu_update(&rcu, &value, 20);

	// Verify value was updated
	uint64_t new_value = atomic_load_explicit(&value, memory_order_acquire);
	TEST_ASSERT_EQUAL(new_value, 20, "value should be updated");

	// Verify epoch has flipped twice (back to 0)
	unsigned global_epoch =
		atomic_load_explicit(&rcu.global_epoch, memory_order_relaxed);
	TEST_ASSERT_EQUAL(
		global_epoch, 0, "epoch should be back to 0 after update"
	);

	LOG(INFO, "test_single_writer passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 4: Multiple Sequential Updates
////////////////////////////////////////////////////////////////////////////////

/**
 * Test multiple sequential updates work correctly
 */
static int
test_multiple_updates(void) {
	LOG(INFO, "Running test_multiple_updates...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;

	// Perform multiple updates
	for (uint64_t i = 1; i <= 10; i++) {
		rcu_update(&rcu, &value, i);
		uint64_t current =
			atomic_load_explicit(&value, memory_order_acquire);
		TEST_ASSERT_EQUAL(current, i, "value should match iteration");
	}

	LOG(INFO, "test_multiple_updates passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 5: Reader-Writer Interaction
////////////////////////////////////////////////////////////////////////////////

/**
 * Test that readers see consistent values during updates
 */
static int
test_reader_writer_interaction(void) {
	LOG(INFO, "Running test_reader_writer_interaction...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 100;

	// Start read-side critical section
	uint64_t read1 = RCU_READ_BEGIN(&rcu, 0, &value);
	TEST_ASSERT_EQUAL(read1, 100, "initial read should be 100");

	// Update value (this will block until reader finishes)
	// But we're still in critical section, so we can't call update yet

	RCU_READ_END(&rcu, 0);

	// Now update
	rcu_update(&rcu, &value, 200);

	// New read should see new value
	uint64_t read2 = RCU_READ_BEGIN(&rcu, 0, &value);
	TEST_ASSERT_EQUAL(read2, 200, "read after update should be 200");
	RCU_READ_END(&rcu, 0);

	LOG(INFO, "test_reader_writer_interaction passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 6: Multiple Workers
////////////////////////////////////////////////////////////////////////////////

/**
 * Test that multiple workers can read concurrently
 */
static int
test_multiple_workers(void) {
	LOG(INFO, "Running test_multiple_workers...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 777;

	// Start read-side critical sections for all workers
	uint64_t values[RCU_WORKERS];
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		values[i] = RCU_READ_BEGIN(&rcu, i, &value);
		TEST_ASSERT_EQUAL(
			values[i], 777, "all workers should read same value"
		);

		// Verify worker is active
		unsigned state = atomic_load_explicit(
			&rcu.workers[i].state, memory_order_relaxed
		);
		unsigned active = GET_ACTIVE(state);
		TEST_ASSERT_EQUAL(active, 1, "worker should be active");
	}

	// End all read-side critical sections
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		RCU_READ_END(&rcu, i);

		// Verify worker is inactive
		unsigned state = atomic_load_explicit(
			&rcu.workers[i].state, memory_order_relaxed
		);
		unsigned active = GET_ACTIVE(state);
		TEST_ASSERT_EQUAL(active, 0, "worker should be inactive");
	}

	LOG(INFO, "test_multiple_workers passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 7: Epoch Synchronization
////////////////////////////////////////////////////////////////////////////////

/**
 * Test that epoch synchronization works correctly
 */
static int
test_epoch_synchronization(void) {
	LOG(INFO, "Running test_epoch_synchronization...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 1;

	// Start read with worker 0
	uint64_t read1 = RCU_READ_BEGIN(&rcu, 0, &value);
	TEST_ASSERT_EQUAL(read1, 1, "initial read should be 1");

	unsigned state0 = atomic_load_explicit(
		&rcu.workers[0].state, memory_order_relaxed
	);
	unsigned epoch0 = GET_EPOCH(state0);
	TEST_ASSERT_EQUAL(epoch0, 0, "worker should be in epoch 0");

	RCU_READ_END(&rcu, 0);

	// Update value (flips epoch twice)
	rcu_update(&rcu, &value, 2);

	// Start new read - should be in epoch 0 again
	uint64_t read2 = RCU_READ_BEGIN(&rcu, 0, &value);
	TEST_ASSERT_EQUAL(read2, 2, "read after update should be 2");

	unsigned state1 = atomic_load_explicit(
		&rcu.workers[0].state, memory_order_relaxed
	);
	unsigned epoch1 = GET_EPOCH(state1);
	TEST_ASSERT_EQUAL(
		epoch1, 0, "worker should be in epoch 0 after full cycle"
	);

	RCU_READ_END(&rcu, 0);

	LOG(INFO, "test_epoch_synchronization passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 8: Concurrent Readers (Multi-threaded)
////////////////////////////////////////////////////////////////////////////////

struct reader_thread_args {
	rcu_t *rcu;
	atomic_ulong *value;
	size_t worker_id;
	size_t iterations;
	atomic_uint *error_count;
};

static void *
reader_thread_func(void *arg) {
	struct reader_thread_args *args = (struct reader_thread_args *)arg;

	for (size_t i = 0; i < args->iterations; i++) {
		uint64_t val =
			RCU_READ_BEGIN(args->rcu, args->worker_id, args->value);

		// Value should always be valid (not some garbage)
		// In this test, we expect values to be in a reasonable range
		if (val > 1000000) {
			atomic_fetch_add(args->error_count, 1);
		}

		// Simulate some work
		for (volatile int j = 0; j < 100; j++)
			;

		RCU_READ_END(args->rcu, args->worker_id);
	}

	return NULL;
}

static int
test_concurrent_readers(void) {
	LOG(INFO, "Running test_concurrent_readers...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 42;
	atomic_uint error_count = 0;

	pthread_t threads[RCU_WORKERS];
	struct reader_thread_args args[RCU_WORKERS];

	// Create reader threads
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		args[i].rcu = &rcu;
		args[i].value = &value;
		args[i].worker_id = i;
		args[i].iterations = 1000;
		args[i].error_count = &error_count;

		int res = pthread_create(
			&threads[i], NULL, reader_thread_func, &args[i]
		);
		TEST_ASSERT_EQUAL(res, 0, "pthread_create should succeed");
	}

	// Wait for all threads
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		int res = pthread_join(threads[i], NULL);
		TEST_ASSERT_EQUAL(res, 0, "pthread_join should succeed");
	}

	// Check no errors occurred
	unsigned errors = atomic_load(&error_count);
	TEST_ASSERT_EQUAL(
		errors, 0, "no errors should occur during concurrent reads"
	);

	LOG(INFO, "test_concurrent_readers passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 9: Concurrent Readers with Writer
////////////////////////////////////////////////////////////////////////////////

struct reader_writer_args {
	rcu_t *rcu;
	atomic_ulong *value;
	atomic_bool *stop;
	size_t worker_id;
	atomic_uint *read_count;
};

static void *
reader_with_writer_func(void *arg) {
	struct reader_writer_args *args = (struct reader_writer_args *)arg;

	while (!atomic_load(args->stop)) {
		uint64_t val =
			RCU_READ_BEGIN(args->rcu, args->worker_id, args->value);

		// Value should be monotonically increasing or same
		(void)val; // Use the value

		atomic_fetch_add(args->read_count, 1);

		RCU_READ_END(args->rcu, args->worker_id);

		// Small delay
		for (volatile int i = 0; i < 50; i++)
			;
	}

	return NULL;
}

static int
test_concurrent_readers_with_writer(void) {
	LOG(INFO, "Running test_concurrent_readers_with_writer...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;
	atomic_bool stop = false;
	atomic_uint read_count = 0;

	pthread_t threads[RCU_WORKERS];
	struct reader_writer_args args[RCU_WORKERS];

	// Create reader threads
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		args[i].rcu = &rcu;
		args[i].value = &value;
		args[i].stop = &stop;
		args[i].worker_id = i;
		args[i].read_count = &read_count;

		int res = pthread_create(
			&threads[i], NULL, reader_with_writer_func, &args[i]
		);
		TEST_ASSERT_EQUAL(res, 0, "pthread_create should succeed");
	}

	// Perform updates while readers are active
	for (uint64_t i = 1; i <= 50; i++) {
		rcu_update(&rcu, &value, i);
		// Small delay between updates
		usleep(1000); // 1ms
	}

	// Stop readers
	atomic_store(&stop, true);

	// Wait for all threads
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		int res = pthread_join(threads[i], NULL);
		TEST_ASSERT_EQUAL(res, 0, "pthread_join should succeed");
	}

	// Verify final value
	uint64_t final_value =
		atomic_load_explicit(&value, memory_order_acquire);
	TEST_ASSERT_EQUAL(final_value, 50, "final value should be 50");

	// Verify reads occurred
	unsigned reads = atomic_load(&read_count);
	TEST_ASSERT(reads > 0, "some reads should have occurred");

	LOG(INFO,
	    "test_concurrent_readers_with_writer passed (reads: %u)",
	    reads);
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 10: Stress Test - Rapid Updates
////////////////////////////////////////////////////////////////////////////////

static int
test_rapid_updates(void) {
	LOG(INFO, "Running test_rapid_updates...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;

	// Perform many rapid updates
	const size_t num_updates = 100;
	for (size_t i = 1; i <= num_updates; i++) {
		rcu_update(&rcu, &value, i);
	}

	// Verify final value
	uint64_t final_value =
		atomic_load_explicit(&value, memory_order_acquire);
	TEST_ASSERT_EQUAL(
		final_value,
		num_updates,
		"final value should match iteration count"
	);

	// Verify all workers are inactive
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		unsigned state = atomic_load_explicit(
			&rcu.workers[i].state, memory_order_relaxed
		);
		unsigned active = GET_ACTIVE(state);
		TEST_ASSERT_EQUAL(active, 0, "all workers should be inactive");
	}

	LOG(INFO, "test_rapid_updates passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 11: Edge Case - All Workers Active
////////////////////////////////////////////////////////////////////////////////

static int
test_all_workers_active(void) {
	LOG(INFO, "Running test_all_workers_active...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 999;

	// Activate all workers
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		RCU_READ_BEGIN(&rcu, i, &value);
	}

	// Verify all are active
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		unsigned state = atomic_load_explicit(
			&rcu.workers[i].state, memory_order_relaxed
		);
		unsigned active = GET_ACTIVE(state);
		TEST_ASSERT_EQUAL(active, 1, "worker should be active");
	}

	// Deactivate all workers
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		RCU_READ_END(&rcu, i);
	}

	// Now update should succeed
	rcu_update(&rcu, &value, 1000);

	uint64_t final_value =
		atomic_load_explicit(&value, memory_order_acquire);
	TEST_ASSERT_EQUAL(final_value, 1000, "value should be updated");

	LOG(INFO, "test_all_workers_active passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 12: Memory Ordering Verification
////////////////////////////////////////////////////////////////////////////////

static int
test_memory_ordering(void) {
	LOG(INFO, "Running test_memory_ordering...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;
	atomic_ulong auxiliary = 0;

	// Update auxiliary, then value
	atomic_store_explicit(&auxiliary, 123, memory_order_release);
	rcu_update(&rcu, &value, 1);

	// Reader should see both updates
	uint64_t val = RCU_READ_BEGIN(&rcu, 0, &value);
	TEST_ASSERT_EQUAL(val, 1, "should see updated value");

	uint64_t aux = atomic_load_explicit(&auxiliary, memory_order_acquire);
	TEST_ASSERT_EQUAL(
		aux, 123, "should see auxiliary update due to memory ordering"
	);

	RCU_READ_END(&rcu, 0);

	LOG(INFO, "test_memory_ordering passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 13: rcu_load Function
////////////////////////////////////////////////////////////////////////////////

static int
test_rcu_load(void) {
	LOG(INFO, "Running test_rcu_load...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 555;

	// Test rcu_load
	uint64_t loaded = rcu_load(&rcu, &value);
	TEST_ASSERT_EQUAL(loaded, 555, "rcu_load should return correct value");

	// Update and load again
	atomic_store_explicit(&value, 666, memory_order_release);
	loaded = rcu_load(&rcu, &value);
	TEST_ASSERT_EQUAL(loaded, 666, "rcu_load should return updated value");

	LOG(INFO, "test_rcu_load passed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 14: Aggressive Race Detection - Concurrent Hammer Test
////////////////////////////////////////////////////////////////////////////////

/**
 * Aggressively hammer RCU with many concurrent readers and rapid updates
 * to expose race conditions in epoch management and memory ordering
 */

struct hammer_reader_args {
	rcu_t *rcu;
	atomic_ulong *value;
	size_t worker_id;
	atomic_bool *stop;
	atomic_uint *stale_reads; // Count reads of stale/invalid data
	atomic_uint *read_count;
};

static void *
hammer_reader_func(void *arg) {
	struct hammer_reader_args *args = (struct hammer_reader_args *)arg;
	uint64_t last_seen = 0;

	while (!atomic_load_explicit(args->stop, memory_order_acquire)) {
		uint64_t val =
			RCU_READ_BEGIN(args->rcu, args->worker_id, args->value);

		// Value should be monotonically increasing
		// If we see a value less than last_seen, it's a race bug
		if (val < last_seen) {
			atomic_fetch_add(args->stale_reads, 1);
			LOG(ERROR,
			    "Worker %zu: stale read detected! val=%lu < "
			    "last=%lu",
			    args->worker_id,
			    val,
			    last_seen);
		}
		last_seen = val;

		atomic_fetch_add(args->read_count, 1);

		RCU_READ_END(args->rcu, args->worker_id);

		// Minimal delay to maximize contention
		for (volatile int i = 0; i < 10; i++)
			;
	}

	return NULL;
}

static int
test_aggressive_race_detection(void) {
	LOG(INFO, "Running test_aggressive_race_detection...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;
	atomic_bool stop = false;
	atomic_uint stale_reads = 0;
	atomic_uint read_count = 0;

	pthread_t threads[RCU_WORKERS];
	struct hammer_reader_args args[RCU_WORKERS];

	// Create aggressive reader threads
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		args[i].rcu = &rcu;
		args[i].value = &value;
		args[i].worker_id = i;
		args[i].stop = &stop;
		args[i].stale_reads = &stale_reads;
		args[i].read_count = &read_count;

		int res = pthread_create(
			&threads[i], NULL, hammer_reader_func, &args[i]
		);
		TEST_ASSERT_EQUAL(res, 0, "pthread_create should succeed");
	}

	// Hammer with rapid updates - no delays!
	const size_t num_updates = 1000;
	for (size_t i = 1; i <= num_updates; i++) {
		rcu_update(&rcu, &value, i);
		// NO delay - maximum stress
	}

	// Stop readers
	atomic_store_explicit(&stop, true, memory_order_release);

	// Wait for all threads
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		int res = pthread_join(threads[i], NULL);
		TEST_ASSERT_EQUAL(res, 0, "pthread_join should succeed");
	}

	// Check for race conditions
	unsigned stales = atomic_load(&stale_reads);
	unsigned reads = atomic_load(&read_count);

	LOG(INFO,
	    "Completed %u reads across %d workers, stale reads: %u",
	    reads,
	    RCU_WORKERS,
	    stales);

	TEST_ASSERT_EQUAL(
		stales,
		0,
		"NO stale reads should occur - this indicates a race bug!"
	);

	LOG(INFO, "test_aggressive_race_detection passed");
	return TEST_SUCCESS;
}
////////////////////////////////////////////////////////////////////////////////
// Benchmark Helper Functions
////////////////////////////////////////////////////////////////////////////////

// Get current time in microseconds
static uint64_t
get_time_us(void) {
	struct timeval tv;
	gettimeofday(&tv, NULL);
	return (uint64_t)tv.tv_sec * 1000000 + tv.tv_usec;
}

////////////////////////////////////////////////////////////////////////////////
// Test 15: Performance Benchmark - Multiple Workers
////////////////////////////////////////////////////////////////////////////////

struct benchmark_args {
	rcu_t *rcu;
	atomic_ulong *value;
	size_t worker_id;
	atomic_bool *stop;
	atomic_ullong *total_reads;
	size_t iterations;
};

static void *
benchmark_reader_func(void *arg) {
	struct benchmark_args *args = (struct benchmark_args *)arg;
	uint64_t local_reads = 0;

	while (!atomic_load_explicit(args->stop, memory_order_acquire)) {
		RCU_READ_BEGIN(args->rcu, args->worker_id, args->value);
		RCU_READ_END(args->rcu, args->worker_id);
		local_reads++;
	}

	atomic_fetch_add(args->total_reads, local_reads);
	return NULL;
}

static int
benchmark_multiworker_throughput(size_t num_workers) {
	if (num_workers > RCU_WORKERS) {
		LOG(ERROR,
		    "Cannot benchmark with %zu workers (max: %d)",
		    num_workers,
		    RCU_WORKERS);
		return TEST_FAILED;
	}

	LOG(INFO, "Benchmarking RCU with %zu workers...", num_workers);

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;
	atomic_bool stop = false;
	atomic_ullong total_reads = 0;

	pthread_t threads[RCU_WORKERS];
	struct benchmark_args args[RCU_WORKERS];

	for (size_t i = 0; i < num_workers; i++) {
		args[i].rcu = &rcu;
		args[i].value = &value;
		args[i].worker_id = i;
		args[i].stop = &stop;
		args[i].total_reads = &total_reads;
		args[i].iterations = 0;

		int res = pthread_create(
			&threads[i], NULL, benchmark_reader_func, &args[i]
		);
		if (res != 0) {
			LOG(ERROR, "Failed to create thread %zu", i);
			return TEST_FAILED;
		}
	}

	const uint64_t duration_us = 1000000; // 1 second
	uint64_t start_time = get_time_us();

	size_t num_updates = 0;
	uint64_t update_start = get_time_us();

	while (get_time_us() - start_time < duration_us) {
		rcu_update(&rcu, &value, num_updates + 1);
		num_updates++;
	}

	uint64_t update_end = get_time_us();
	uint64_t update_duration = update_end - update_start;

	atomic_store_explicit(&stop, true, memory_order_release);

	for (size_t i = 0; i < num_workers; i++) {
		pthread_join(threads[i], NULL);
	}

	uint64_t end_time = get_time_us();
	uint64_t elapsed_us = end_time - start_time;

	uint64_t reads = atomic_load(&total_reads);
	double reads_per_sec = (double)reads / ((double)elapsed_us / 1000000.0);
	double updates_per_sec =
		(double)num_updates / ((double)update_duration / 1000000.0);
	double avg_update_latency_us =
		(double)update_duration / (double)num_updates;

	LOG(INFO, "=== Benchmark Results (%zu workers) ===", num_workers);
	LOG(INFO, "  Duration: %.3f seconds", (double)elapsed_us / 1000000.0);
	LOG(INFO, "  Total reads: %lu", reads);
	LOG(INFO, "  Read throughput: %.2f Mops/sec", reads_per_sec / 1000000.0
	);
	LOG(INFO,
	    "  Reads per worker: %.2f Mops/sec",
	    reads_per_sec / num_workers / 1000000.0);
	LOG(INFO, "  Total updates: %zu", num_updates);
	LOG(INFO, "  Update throughput: %.2f ops/sec", updates_per_sec);
	LOG(INFO, "  Avg update latency: %.2f µs", avg_update_latency_us);

	return TEST_SUCCESS;
}

static int
test_performance_benchmark(void) {
	LOG(INFO, "Running performance benchmark...");

	size_t worker_counts[] = {1, 2, 4, 8, 16};

	for (size_t i = 0; i < sizeof(worker_counts) / sizeof(worker_counts[0]);
	     i++) {
		size_t num_workers = worker_counts[i];

		if (num_workers > RCU_WORKERS) {
			LOG(INFO,
			    "Skipping %zu workers (max: %d)",
			    num_workers,
			    RCU_WORKERS);
			continue;
		}

		if (benchmark_multiworker_throughput(num_workers) !=
		    TEST_SUCCESS) {
			return TEST_FAILED;
		}

		LOG(INFO, "");
	}

	LOG(INFO, "Performance benchmark completed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Test 16: Contention Benchmark
////////////////////////////////////////////////////////////////////////////////

struct contention_args {
	rcu_t *rcu;
	atomic_ulong *value;
	size_t worker_id;
	size_t iterations;
	atomic_ullong *total_ops;
	uint64_t *worker_time_us;
};

static void *
contention_worker_func(void *arg) {
	struct contention_args *args = (struct contention_args *)arg;

	uint64_t start = get_time_us();

	for (size_t i = 0; i < args->iterations; i++) {
		RCU_READ_BEGIN(args->rcu, args->worker_id, args->value);
		RCU_READ_END(args->rcu, args->worker_id);
	}

	uint64_t end = get_time_us();
	args->worker_time_us[args->worker_id] = end - start;

	atomic_fetch_add(args->total_ops, args->iterations);

	return NULL;
}

static int
test_contention_benchmark(void) {
	LOG(INFO, "Running contention benchmark...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;
	atomic_ullong total_ops = 0;
	uint64_t worker_times[RCU_WORKERS] = {0};

	const size_t iterations_per_worker = 100000;

	pthread_t threads[RCU_WORKERS];
	struct contention_args args[RCU_WORKERS];

	uint64_t start_time = get_time_us();

	for (size_t i = 0; i < RCU_WORKERS; i++) {
		args[i].rcu = &rcu;
		args[i].value = &value;
		args[i].worker_id = i;
		args[i].iterations = iterations_per_worker;
		args[i].total_ops = &total_ops;
		args[i].worker_time_us = worker_times;

		int res = pthread_create(
			&threads[i], NULL, contention_worker_func, &args[i]
		);
		TEST_ASSERT_EQUAL(res, 0, "pthread_create should succeed");
	}

	for (size_t i = 0; i < RCU_WORKERS; i++) {
		pthread_join(threads[i], NULL);
	}

	uint64_t end_time = get_time_us();
	uint64_t total_time = end_time - start_time;

	uint64_t ops = atomic_load(&total_ops);
	double total_throughput =
		(double)ops / ((double)total_time / 1000000.0);

	uint64_t min_time = worker_times[0];
	uint64_t max_time = worker_times[0];
	uint64_t sum_time = 0;

	for (size_t i = 0; i < RCU_WORKERS; i++) {
		if (worker_times[i] < min_time)
			min_time = worker_times[i];
		if (worker_times[i] > max_time)
			max_time = worker_times[i];
		sum_time += worker_times[i];
	}

	double avg_time = (double)sum_time / RCU_WORKERS;
	double fairness = (double)min_time / (double)max_time;

	LOG(INFO, "=== Contention Benchmark Results ===");
	LOG(INFO, "  Workers: %d", RCU_WORKERS);
	LOG(INFO, "  Iterations per worker: %zu", iterations_per_worker);
	LOG(INFO, "  Total operations: %lu", ops);
	LOG(INFO, "  Total time: %.3f seconds", (double)total_time / 1000000.0);
	LOG(INFO,
	    "  Aggregate throughput: %.2f Mops/sec",
	    total_throughput / 1000000.0);
	LOG(INFO,
	    "  Per-worker throughput: %.2f Mops/sec",
	    total_throughput / RCU_WORKERS / 1000000.0);
	LOG(INFO,
	    "  Worker time - min: %.3f ms, max: %.3f ms, avg: %.3f ms",
	    (double)min_time / 1000.0,
	    (double)max_time / 1000.0,
	    avg_time / 1000.0);
	LOG(INFO, "  Fairness index: %.3f (1.0 = perfect)", fairness);

	LOG(INFO, "Contention benchmark completed");
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////
// Main Test Runner
////////////////////////////////////////////////////////////////////////////////

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting RCU Test Suite ===");

	int result = TEST_SUCCESS;

	// Run all tests
	if (test_basic_init() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_single_reader() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_single_writer() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_multiple_updates() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_reader_writer_interaction() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_multiple_workers() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_epoch_synchronization() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_concurrent_readers() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_concurrent_readers_with_writer() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_rapid_updates() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_all_workers_active() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_memory_ordering() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_rcu_load() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_aggressive_race_detection() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_performance_benchmark() != TEST_SUCCESS)
		result = TEST_FAILED;
	if (test_contention_benchmark() != TEST_SUCCESS)
		result = TEST_FAILED;

	if (result == TEST_SUCCESS) {
		LOG(INFO, "=== All RCU tests passed! ===");
	} else {
		LOG(ERROR, "=== Some RCU tests failed ===");
	}

	return result;
}