/**
 * @file rcu_bench.c
 * @brief Performance benchmark suite for RCU (Read-Copy-Update) mechanism
 *
 * This benchmark suite measures RCU performance characteristics:
 * - Read throughput with varying worker counts
 * - Update latency and throughput
 * - Scalability metrics
 * - Contention behavior and fairness
 * - Reader/writer interaction performance
 *
 * Run with: ./rcu_bench
 */

#include "common/rcu.h"
#include "lib/logging/log.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <sys/time.h>
#include <unistd.h>

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
// Benchmark 1: Throughput with Multiple Workers
////////////////////////////////////////////////////////////////////////////////

struct benchmark_args {
	rcu_t *rcu;
	atomic_ulong *value;
	size_t worker_id;
	atomic_bool *stop;
	atomic_ullong *total_reads;
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

static void
benchmark_multiworker_throughput(size_t num_workers) {
	if (num_workers > RCU_WORKERS) {
		LOG(ERROR,
		    "Cannot benchmark with %zu workers (max: %d)",
		    num_workers,
		    RCU_WORKERS);
		return;
	}

	LOG(INFO, "Benchmarking RCU with %zu workers...", num_workers);

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;
	atomic_bool stop = false;
	atomic_ullong total_reads = 0;

	pthread_t threads[RCU_WORKERS];
	struct benchmark_args args[RCU_WORKERS];

	// Create reader threads
	for (size_t i = 0; i < num_workers; i++) {
		args[i].rcu = &rcu;
		args[i].value = &value;
		args[i].worker_id = i;
		args[i].stop = &stop;
		args[i].total_reads = &total_reads;

		int res = pthread_create(
			&threads[i], NULL, benchmark_reader_func, &args[i]
		);
		if (res != 0) {
			LOG(ERROR, "Failed to create thread %zu", i);
			return;
		}
	}

	// Run benchmark for fixed duration
	const uint64_t duration_us = 2000000; // 2 seconds
	uint64_t start_time = get_time_us();

	size_t num_updates = 0;
	uint64_t update_start = get_time_us();

	while (get_time_us() - start_time < duration_us) {
		rcu_update(&rcu, &value, num_updates + 1);
		num_updates++;
	}

	uint64_t update_end = get_time_us();
	uint64_t update_duration = update_end - update_start;

	// Stop readers
	atomic_store_explicit(&stop, true, memory_order_release);

	// Wait for all threads
	for (size_t i = 0; i < num_workers; i++) {
		pthread_join(threads[i], NULL);
	}

	uint64_t end_time = get_time_us();
	uint64_t elapsed_us = end_time - start_time;

	// Calculate and report metrics
	uint64_t reads = atomic_load(&total_reads);
	double reads_per_sec = (double)reads / ((double)elapsed_us / 1000000.0);
	double updates_per_sec =
		(double)num_updates / ((double)update_duration / 1000000.0);
	double avg_update_latency_us =
		(double)update_duration / (double)num_updates;

	LOG(INFO,
	    "=== Throughput Benchmark Results (%zu workers) ===",
	    num_workers);
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
	LOG(INFO, "");
}

////////////////////////////////////////////////////////////////////////////////
// Benchmark 2: Contention and Fairness
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

static void
benchmark_contention(void) {
	LOG(INFO, "Running contention benchmark...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;
	atomic_ullong total_ops = 0;
	uint64_t worker_times[RCU_WORKERS] = {0};

	const size_t iterations_per_worker = 500000;

	pthread_t threads[RCU_WORKERS];
	struct contention_args args[RCU_WORKERS];

	uint64_t start_time = get_time_us();

	// Create all workers simultaneously for maximum contention
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
		if (res != 0) {
			LOG(ERROR, "Failed to create thread %zu", i);
			return;
		}
	}

	// Wait for all workers
	for (size_t i = 0; i < RCU_WORKERS; i++) {
		pthread_join(threads[i], NULL);
	}

	uint64_t end_time = get_time_us();
	uint64_t total_time = end_time - start_time;

	// Calculate statistics
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
	LOG(INFO, "");
}

////////////////////////////////////////////////////////////////////////////////
// Benchmark 3: Latency Distribution
////////////////////////////////////////////////////////////////////////////////

static void
benchmark_latency_distribution(void) {
	LOG(INFO, "Running latency distribution benchmark...");

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;

	const size_t num_samples = 10000;
	uint64_t latencies[num_samples];

	// Measure read latencies
	for (size_t i = 0; i < num_samples; i++) {
		uint64_t start = get_time_us();
		RCU_READ_BEGIN(&rcu, 0, &value);
		RCU_READ_END(&rcu, 0);
		uint64_t end = get_time_us();
		latencies[i] = end - start;
	}

	// Calculate statistics
	uint64_t min_lat = latencies[0];
	uint64_t max_lat = latencies[0];
	uint64_t sum_lat = 0;

	for (size_t i = 0; i < num_samples; i++) {
		if (latencies[i] < min_lat)
			min_lat = latencies[i];
		if (latencies[i] > max_lat)
			max_lat = latencies[i];
		sum_lat += latencies[i];
	}

	double avg_lat = (double)sum_lat / num_samples;

	// Calculate percentiles (simple sort)
	for (size_t i = 0; i < num_samples - 1; i++) {
		for (size_t j = i + 1; j < num_samples; j++) {
			if (latencies[j] < latencies[i]) {
				uint64_t tmp = latencies[i];
				latencies[i] = latencies[j];
				latencies[j] = tmp;
			}
		}
	}

	uint64_t p50 = latencies[num_samples / 2];
	uint64_t p95 = latencies[(num_samples * 95) / 100];
	uint64_t p99 = latencies[(num_samples * 99) / 100];

	LOG(INFO, "=== Latency Distribution Results ===");
	LOG(INFO, "  Samples: %zu", num_samples);
	LOG(INFO, "  Min latency: %lu µs", min_lat);
	LOG(INFO, "  Avg latency: %.2f µs", avg_lat);
	LOG(INFO, "  P50 latency: %lu µs", p50);
	LOG(INFO, "  P95 latency: %lu µs", p95);
	LOG(INFO, "  P99 latency: %lu µs", p99);
	LOG(INFO, "  Max latency: %lu µs", max_lat);
	LOG(INFO, "");
}

////////////////////////////////////////////////////////////////////////////////
// Benchmark 4: Reader/Writer Thread Interaction
////////////////////////////////////////////////////////////////////////////////

struct reader_writer_bench_args {
	rcu_t *rcu;
	atomic_ulong *value;
	size_t worker_id;
	atomic_bool *stop;
	atomic_ullong *total_reads;
};

struct writer_bench_args {
	rcu_t *rcu;
	atomic_ulong *value;
	atomic_bool *stop;
	atomic_ullong *total_updates;
	uint64_t *update_latencies;
	size_t max_latencies;
};

static void *
reader_writer_bench_func(void *arg) {
	struct reader_writer_bench_args *args =
		(struct reader_writer_bench_args *)arg;
	uint64_t local_reads = 0;

	while (!atomic_load_explicit(args->stop, memory_order_acquire)) {
		RCU_READ_BEGIN(args->rcu, args->worker_id, args->value);
		// Simulate some work
		for (volatile int i = 0; i < 10; i++)
			;
		RCU_READ_END(args->rcu, args->worker_id);
		local_reads++;
	}

	atomic_fetch_add(args->total_reads, local_reads);
	return NULL;
}

static void *
writer_bench_func(void *arg) {
	struct writer_bench_args *args = (struct writer_bench_args *)arg;
	uint64_t local_updates = 0;
	size_t latency_idx = 0;

	while (!atomic_load_explicit(args->stop, memory_order_acquire)) {
		uint64_t start = get_time_us();
		rcu_update(args->rcu, args->value, local_updates + 1);
		uint64_t end = get_time_us();

		if (latency_idx < args->max_latencies) {
			args->update_latencies[latency_idx++] = end - start;
		}

		local_updates++;

		// Small delay between updates
		usleep(100); // 100 microseconds
	}

	atomic_fetch_add(args->total_updates, local_updates);
	return NULL;
}

static void
benchmark_reader_writer_interaction(size_t num_readers) {
	if (num_readers >= RCU_WORKERS) {
		LOG(ERROR,
		    "Need at least 1 worker for writer (readers: %zu, max: %d)",
		    num_readers,
		    RCU_WORKERS - 1);
		return;
	}

	LOG(INFO,
	    "Benchmarking reader/writer interaction (%zu readers, 1 writer)...",
	    num_readers);

	rcu_t rcu;
	rcu_init(&rcu);
	atomic_ulong value = 0;
	atomic_bool stop = false;
	atomic_ullong total_reads = 0;
	atomic_ullong total_updates = 0;

	const size_t max_latencies = 10000;
	uint64_t update_latencies[max_latencies];

	pthread_t reader_threads[RCU_WORKERS];
	pthread_t writer_thread;
	struct reader_writer_bench_args reader_args[RCU_WORKERS];
	struct writer_bench_args writer_args;

	// Create reader threads
	for (size_t i = 0; i < num_readers; i++) {
		reader_args[i].rcu = &rcu;
		reader_args[i].value = &value;
		reader_args[i].worker_id = i;
		reader_args[i].stop = &stop;
		reader_args[i].total_reads = &total_reads;

		int res = pthread_create(
			&reader_threads[i],
			NULL,
			reader_writer_bench_func,
			&reader_args[i]
		);
		if (res != 0) {
			LOG(ERROR, "Failed to create reader thread %zu", i);
			return;
		}
	}

	// Create writer thread
	writer_args.rcu = &rcu;
	writer_args.value = &value;
	writer_args.stop = &stop;
	writer_args.total_updates = &total_updates;
	writer_args.update_latencies = update_latencies;
	writer_args.max_latencies = max_latencies;

	int res = pthread_create(
		&writer_thread, NULL, writer_bench_func, &writer_args
	);
	if (res != 0) {
		LOG(ERROR, "Failed to create writer thread");
		return;
	}

	// Run for fixed duration
	uint64_t start_time = get_time_us();
	sleep(3); // 3 seconds
	uint64_t end_time = get_time_us();
	uint64_t elapsed_us = end_time - start_time;

	// Stop all threads
	atomic_store_explicit(&stop, true, memory_order_release);

	// Wait for threads
	for (size_t i = 0; i < num_readers; i++) {
		pthread_join(reader_threads[i], NULL);
	}
	pthread_join(writer_thread, NULL);

	// Calculate metrics
	uint64_t reads = atomic_load(&total_reads);
	uint64_t updates = atomic_load(&total_updates);
	double reads_per_sec = (double)reads / ((double)elapsed_us / 1000000.0);
	double updates_per_sec =
		(double)updates / ((double)elapsed_us / 1000000.0);

	// Calculate update latency statistics
	uint64_t min_update_lat = update_latencies[0];
	uint64_t max_update_lat = update_latencies[0];
	uint64_t sum_update_lat = 0;
	size_t num_latencies =
		(updates < max_latencies) ? updates : max_latencies;

	for (size_t i = 0; i < num_latencies; i++) {
		if (update_latencies[i] < min_update_lat)
			min_update_lat = update_latencies[i];
		if (update_latencies[i] > max_update_lat)
			max_update_lat = update_latencies[i];
		sum_update_lat += update_latencies[i];
	}

	double avg_update_lat = (double)sum_update_lat / num_latencies;

	LOG(INFO, "=== Reader/Writer Interaction Results ===");
	LOG(INFO, "  Readers: %zu, Writer: 1", num_readers);
	LOG(INFO, "  Duration: %.3f seconds", (double)elapsed_us / 1000000.0);
	LOG(INFO,
	    "  Total reads: %lu (%.2f Mops/sec)",
	    reads,
	    reads_per_sec / 1000000.0);
	LOG(INFO,
	    "  Reads per reader: %.2f Mops/sec",
	    reads_per_sec / num_readers / 1000000.0);
	LOG(INFO,
	    "  Total updates: %lu (%.2f ops/sec)",
	    updates,
	    updates_per_sec);
	LOG(INFO,
	    "  Update latency - min: %lu µs, avg: %.2f µs, max: %lu µs",
	    min_update_lat,
	    avg_update_lat,
	    max_update_lat);
	LOG(INFO, "  Read/Update ratio: %.2f:1", (double)reads / updates);
	LOG(INFO, "");
}

////////////////////////////////////////////////////////////////////////////////
// Main Benchmark Runner
////////////////////////////////////////////////////////////////////////////////

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== RCU Performance Benchmark Suite ===");
	LOG(INFO, "RCU_WORKERS: %d", RCU_WORKERS);
	LOG(INFO, "");

	// Benchmark 1: Throughput with varying worker counts
	LOG(INFO, "--- Benchmark 1: Throughput Scalability ---");
	size_t worker_counts[] = {1, 2, 4, 8};
	for (size_t i = 0; i < sizeof(worker_counts) / sizeof(worker_counts[0]);
	     i++) {
		if (worker_counts[i] <= RCU_WORKERS) {
			benchmark_multiworker_throughput(worker_counts[i]);
		}
	}

	// Benchmark 2: Contention and fairness
	LOG(INFO, "--- Benchmark 2: Contention and Fairness ---");
	benchmark_contention();

	// Benchmark 3: Latency distribution
	LOG(INFO, "--- Benchmark 3: Latency Distribution ---");
	benchmark_latency_distribution();

	// Benchmark 4: Reader/writer interaction
	LOG(INFO, "--- Benchmark 4: Reader/Writer Interaction ---");
	size_t reader_counts[] = {1, 2, 4, 7};
	for (size_t i = 0; i < sizeof(reader_counts) / sizeof(reader_counts[0]);
	     i++) {
		if (reader_counts[i] < RCU_WORKERS) {
			benchmark_reader_writer_interaction(reader_counts[i]);
		}
	}

	LOG(INFO, "=== Benchmark Suite Completed ===");

	return 0;
}