/**
 * @file btree_bench_u64.c
 * @brief Performance benchmark for btree_u64
 *
 * This benchmark measures btree_u64 search performance with:
 * - Configurable number of elements (default: 4M)
 * - 1M random searches per iteration
 * - 10 iterations for statistical significance
 * - Uses hugepages for optimal memory performance
 *
 * Prerequisites:
 *   sudo sysctl -w vm.nr_hugepages=256
 *
 * Usage:
 *   ./btree_bench_u64 [num_elements]
 *
 * Examples:
 *   ./btree_bench_u64           # Use default 4M elements
 *   ./btree_bench_u64 1000000   # Use 1M elements
 *   ./btree_bench_u64 10000000  # Use 10M elements
 */

#include "common/btree/u64.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "lib/logging/log.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

////////////////////////////////////////////////////////////////////////////////
// Configuration
////////////////////////////////////////////////////////////////////////////////

#define DEFAULT_BTREE_ELEMENTS 4000000 // Default: 4M elements
#define BATCH_SIZE 32
#define SEARCHES_PER_ITER BATCH_SIZE * 100000 // 3.2M searches per iteration
#define NUM_ITERATIONS 10		      // 10 iterations
#define ARENA_SIZE (1ULL << 28)		      // 256 MiB

////////////////////////////////////////////////////////////////////////////////
// Helper Functions
////////////////////////////////////////////////////////////////////////////////

/**
 * Get current time in nanoseconds
 */
static uint64_t
get_time_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000000000ULL + ts.tv_nsec;
}

/**
 * Simple LCG random number generator for reproducible results
 */
static uint64_t
lcg_rand(uint64_t *state) {
	*state = (*state * 6364136223846793005ULL + 1442695040888963407ULL);
	return *state;
}

/**
 * Allocate memory using hugepages
 */
static void *
allocate_hugepage_memory(size_t size) {
	void *mem =
		mmap(NULL,
		     size,
		     PROT_READ | PROT_WRITE,
		     MAP_PRIVATE | MAP_ANONYMOUS | MAP_HUGETLB | MAP_POPULATE,
		     -1,
		     0);

	if (mem == MAP_FAILED) {
		LOG(ERROR, "Failed to allocate %zu bytes from hugepages", size);
		LOG(ERROR,
		    "Make sure hugepages are configured: sudo sysctl -w "
		    "vm.nr_hugepages=256");
		return NULL;
	}

	return mem;
}

/**
 * Setup memory allocator with hugepage-backed arena
 */
static int
setup_allocator(
	struct block_allocator *ba,
	struct memory_context *mctx,
	void **raw_mem,
	size_t size
) {
	if (block_allocator_init(ba) != 0) {
		LOG(ERROR, "block_allocator_init failed");
		return -1;
	}

	*raw_mem = allocate_hugepage_memory(size);
	if (*raw_mem == NULL) {
		return -1;
	}

	LOG(INFO, "Allocated %zu MB arena using hugepages", size / (1024 * 1024)
	);

	block_allocator_put_arena(ba, *raw_mem, size);

	if (memory_context_init(mctx, "btree_bench_u64", ba) != 0) {
		LOG(ERROR, "memory_context_init failed");
		munmap(*raw_mem, size);
		return -1;
	}

	return 0;
}

////////////////////////////////////////////////////////////////////////////////
// Benchmark: btree_u64
////////////////////////////////////////////////////////////////////////////////

static void
benchmark_btree_u64(size_t num_elements) {
	LOG(INFO, "=== Benchmarking btree_u64 ===");
	LOG(INFO, "Elements: %zu", num_elements);
	LOG(INFO, "Searches per iteration: %d", SEARCHES_PER_ITER);
	LOG(INFO, "Iterations: %d", NUM_ITERATIONS);
	LOG(INFO, "");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;

	// Setup allocator
	if (setup_allocator(&ba, &mctx, &raw_mem, ARENA_SIZE) != 0) {
		LOG(ERROR, "Failed to setup allocator");
		return;
	}

	// Allocate and generate sorted data from hugepages: 0, 2, 4, 6, ...
	LOG(INFO, "Generating %zu uint64_t elements...", num_elements);
	size_t data_size = num_elements * sizeof(uint64_t);
	uint64_t *data = (uint64_t *)allocate_hugepage_memory(data_size);
	if (data == NULL) {
		munmap(raw_mem, ARENA_SIZE);
		return;
	}
	LOG(INFO,
	    "Allocated %zu MB for data using hugepages",
	    data_size / (1024 * 1024));

	for (size_t i = 0; i < num_elements; i++) {
		data[i] = (uint64_t)(i * 2);
	}

	// Build btree
	LOG(INFO, "Building btree_u64...");
	struct btree_u64 tree;
	uint64_t build_start = get_time_ns();
	int ret = btree_u64_init(&tree, data, num_elements, &mctx);
	uint64_t build_end = get_time_ns();

	if (ret != 0) {
		LOG(ERROR, "Failed to initialize btree_u64");
		munmap(data, data_size);
		munmap(raw_mem, ARENA_SIZE);
		return;
	}

	double build_time_ms = (build_end - build_start) / 1000000.0;
	LOG(INFO, "Btree built in %.2f ms", build_time_ms);
	LOG(INFO, "");

	// Allocate and prepare random search values from hugepages
	uint64_t rng_state = 0x123456789ABCDEFULL;
	size_t search_size = SEARCHES_PER_ITER * sizeof(uint64_t);
	uint64_t *search_values =
		(uint64_t *)allocate_hugepage_memory(search_size);
	if (search_values == NULL) {
		btree_u64_free(&tree);
		munmap(data, data_size);
		munmap(raw_mem, ARENA_SIZE);
		return;
	}
	LOG(INFO,
	    "Allocated %zu MB for search values using hugepages",
	    search_size / (1024 * 1024));

	for (size_t i = 0; i < SEARCHES_PER_ITER; i++) {
		// Generate random values in range [0, 2*num_elements) to test
		// hits and misses
		search_values[i] = lcg_rand(&rng_state) % (num_elements * 2);
	}

	// Run benchmark iterations
	LOG(INFO,
	    "Running %d iterations of %d searches...",
	    NUM_ITERATIONS,
	    SEARCHES_PER_ITER);
	uint64_t total_time_ns = 0;
	uint64_t min_time_ns = UINT64_MAX;
	uint64_t max_time_ns = 0;

	for (int iter = 0; iter < NUM_ITERATIONS; iter++) {
		// Warmup: perform same number of searches to warm up caches
		uint64_t warmup_start = get_time_ns();

		uint32_t result[BATCH_SIZE];

		for (size_t i = 0; i < SEARCHES_PER_ITER; i += BATCH_SIZE) {
			volatile size_t idx = btree_u64_lower_bounds(
				&tree, search_values + i, BATCH_SIZE, result
			);
			(void)idx; // Prevent optimization
		}

		uint64_t warmup_end = get_time_ns();
		uint64_t warmup_time = warmup_end - warmup_start;
		double warmup_time_ms = warmup_time / 1000000.0;
		double warmup_searches_per_sec = (double)SEARCHES_PER_ITER /
						 (warmup_time / 1000000000.0);

		// Measurement: perform searches and measure time
		uint64_t iter_start = get_time_ns();

		for (size_t i = 0; i < SEARCHES_PER_ITER; i += BATCH_SIZE) {
			volatile size_t idx = btree_u64_lower_bounds(
				&tree, search_values + i, BATCH_SIZE, result
			);
			(void)idx; // Prevent optimization
		}

		uint64_t iter_end = get_time_ns();
		uint64_t iter_time = iter_end - iter_start;

		total_time_ns += iter_time;
		if (iter_time < min_time_ns)
			min_time_ns = iter_time;
		if (iter_time > max_time_ns)
			max_time_ns = iter_time;

		double iter_time_ms = iter_time / 1000000.0;
		double searches_per_sec =
			(double)SEARCHES_PER_ITER / (iter_time / 1000000000.0);
		LOG(INFO,
		    "  Iteration %2d: warmup %.2f ms (%.2f M/s), measurement "
		    "%.2f ms (%.2f M/s)",
		    iter + 1,
		    warmup_time_ms,
		    warmup_searches_per_sec / 1000000.0,
		    iter_time_ms,
		    searches_per_sec / 1000000.0);
	}

	// Calculate statistics
	double avg_time_ms = (total_time_ns / NUM_ITERATIONS) / 1000000.0;
	double min_time_ms = min_time_ns / 1000000.0;
	double max_time_ms = max_time_ns / 1000000.0;
	double total_searches = (double)SEARCHES_PER_ITER * NUM_ITERATIONS;
	double total_time_sec = total_time_ns / 1000000000.0;
	double avg_throughput = total_searches / total_time_sec;
	double avg_latency_ns = (double)total_time_ns / total_searches;

	LOG(INFO, "");
	LOG(INFO, "=== btree_u64 Results ===");
	LOG(INFO, "Total searches: %.0f", total_searches);
	LOG(INFO, "Total time: %.2f seconds", total_time_sec);
	LOG(INFO, "Average time per iteration: %.2f ms", avg_time_ms);
	LOG(INFO, "Min iteration time: %.2f ms", min_time_ms);
	LOG(INFO, "Max iteration time: %.2f ms", max_time_ms);
	LOG(INFO,
	    "Average throughput: %.2f M searches/sec",
	    avg_throughput / 1000000.0);
	LOG(INFO, "Average latency: %.2f ns/search", avg_latency_ns);
	LOG(INFO, "");

	// Cleanup
	munmap(search_values, search_size);
	btree_u64_free(&tree);
	munmap(data, data_size);
	munmap(raw_mem, ARENA_SIZE);
}

////////////////////////////////////////////////////////////////////////////////
// Benchmark: btree_u64 upper_bounds
////////////////////////////////////////////////////////////////////////////////

static void
benchmark_btree_u64_upper_bounds(size_t num_elements) {
	LOG(INFO, "=== Benchmarking btree_u64 upper_bounds ===");
	LOG(INFO, "Elements: %zu", num_elements);
	LOG(INFO, "Searches per iteration: %d", SEARCHES_PER_ITER);
	LOG(INFO, "Iterations: %d", NUM_ITERATIONS);
	LOG(INFO, "");

	struct block_allocator ba;
	struct memory_context mctx;
	void *raw_mem = NULL;

	// Setup allocator
	if (setup_allocator(&ba, &mctx, &raw_mem, ARENA_SIZE) != 0) {
		LOG(ERROR, "Failed to setup allocator");
		return;
	}

	// Allocate and generate sorted data from hugepages: 0, 2, 4, 6, ...
	LOG(INFO, "Generating %zu uint64_t elements...", num_elements);
	size_t data_size = num_elements * sizeof(uint64_t);
	uint64_t *data = (uint64_t *)allocate_hugepage_memory(data_size);
	if (data == NULL) {
		munmap(raw_mem, ARENA_SIZE);
		return;
	}
	LOG(INFO,
	    "Allocated %zu MB for data using hugepages",
	    data_size / (1024 * 1024));

	for (size_t i = 0; i < num_elements; i++) {
		data[i] = (uint64_t)(i * 2);
	}

	// Build btree
	LOG(INFO, "Building btree_u64...");
	struct btree_u64 tree;
	uint64_t build_start = get_time_ns();
	int ret = btree_u64_init(&tree, data, num_elements, &mctx);
	uint64_t build_end = get_time_ns();

	if (ret != 0) {
		LOG(ERROR, "Failed to initialize btree_u64");
		munmap(data, data_size);
		munmap(raw_mem, ARENA_SIZE);
		return;
	}

	double build_time_ms = (build_end - build_start) / 1000000.0;
	LOG(INFO, "Btree built in %.2f ms", build_time_ms);
	LOG(INFO, "");

	// Allocate and prepare random search values from hugepages
	uint64_t rng_state = 0x123456789ABCDEFULL;
	size_t search_size = SEARCHES_PER_ITER * sizeof(uint64_t);
	uint64_t *search_values =
		(uint64_t *)allocate_hugepage_memory(search_size);
	if (search_values == NULL) {
		btree_u64_free(&tree);
		munmap(data, data_size);
		munmap(raw_mem, ARENA_SIZE);
		return;
	}
	LOG(INFO,
	    "Allocated %zu MB for search values using hugepages",
	    search_size / (1024 * 1024));

	for (size_t i = 0; i < SEARCHES_PER_ITER; i++) {
		// Generate random values in range [0, 2*num_elements) to test
		// hits and misses
		search_values[i] = lcg_rand(&rng_state) % (num_elements * 2);
	}

	// Run benchmark iterations
	LOG(INFO,
	    "Running %d iterations of %d searches...",
	    NUM_ITERATIONS,
	    SEARCHES_PER_ITER);
	uint64_t total_time_ns = 0;
	uint64_t min_time_ns = UINT64_MAX;
	uint64_t max_time_ns = 0;

	for (int iter = 0; iter < NUM_ITERATIONS; iter++) {
		// Warmup: perform same number of searches to warm up caches
		uint64_t warmup_start = get_time_ns();

		uint32_t result[BATCH_SIZE];

		for (size_t i = 0; i < SEARCHES_PER_ITER; i += BATCH_SIZE) {
			volatile size_t idx = btree_u64_upper_bounds(
				&tree, search_values + i, BATCH_SIZE, result
			);
			(void)idx; // Prevent optimization
		}

		uint64_t warmup_end = get_time_ns();
		uint64_t warmup_time = warmup_end - warmup_start;
		double warmup_time_ms = warmup_time / 1000000.0;
		double warmup_searches_per_sec = (double)SEARCHES_PER_ITER /
						 (warmup_time / 1000000000.0);

		// Measurement: perform searches and measure time
		uint64_t iter_start = get_time_ns();

		for (size_t i = 0; i < SEARCHES_PER_ITER; i += BATCH_SIZE) {
			volatile size_t idx = btree_u64_upper_bounds(
				&tree, search_values + i, BATCH_SIZE, result
			);
			(void)idx; // Prevent optimization
		}

		uint64_t iter_end = get_time_ns();
		uint64_t iter_time = iter_end - iter_start;

		total_time_ns += iter_time;
		if (iter_time < min_time_ns)
			min_time_ns = iter_time;
		if (iter_time > max_time_ns)
			max_time_ns = iter_time;

		double iter_time_ms = iter_time / 1000000.0;
		double searches_per_sec =
			(double)SEARCHES_PER_ITER / (iter_time / 1000000000.0);
		LOG(INFO,
		    "  Iteration %2d: warmup %.2f ms (%.2f M/s), measurement "
		    "%.2f ms (%.2f M/s)",
		    iter + 1,
		    warmup_time_ms,
		    warmup_searches_per_sec / 1000000.0,
		    iter_time_ms,
		    searches_per_sec / 1000000.0);
	}

	// Calculate statistics
	double avg_time_ms = (total_time_ns / NUM_ITERATIONS) / 1000000.0;
	double min_time_ms = min_time_ns / 1000000.0;
	double max_time_ms = max_time_ns / 1000000.0;
	double total_searches = (double)SEARCHES_PER_ITER * NUM_ITERATIONS;
	double total_time_sec = total_time_ns / 1000000000.0;
	double avg_throughput = total_searches / total_time_sec;
	double avg_latency_ns = (double)total_time_ns / total_searches;

	LOG(INFO, "");
	LOG(INFO, "=== btree_u64 upper_bounds Results ===");
	LOG(INFO, "Total searches: %.0f", total_searches);
	LOG(INFO, "Total time: %.2f seconds", total_time_sec);
	LOG(INFO, "Average time per iteration: %.2f ms", avg_time_ms);
	LOG(INFO, "Min iteration time: %.2f ms", min_time_ms);
	LOG(INFO, "Max iteration time: %.2f ms", max_time_ms);
	LOG(INFO,
	    "Average throughput: %.2f M searches/sec",
	    avg_throughput / 1000000.0);
	LOG(INFO, "Average latency: %.2f ns/search", avg_latency_ns);
	LOG(INFO, "");

	// Cleanup
	munmap(search_values, search_size);
	btree_u64_free(&tree);
	munmap(data, data_size);
	munmap(raw_mem, ARENA_SIZE);
}

////////////////////////////////////////////////////////////////////////////////
// Main
////////////////////////////////////////////////////////////////////////////////

int
main(int argc, char *argv[]) {
	log_enable_name("info");

	// Parse command line arguments
	size_t num_elements = DEFAULT_BTREE_ELEMENTS;
	if (argc > 1) {
		char *endptr;
		long parsed = strtol(argv[1], &endptr, 10);
		if (*endptr != '\0' || parsed <= 0) {
			fprintf(stderr,
				"Error: Invalid number of elements: %s\n",
				argv[1]);
			fprintf(stderr, "Usage: %s [num_elements]\n", argv[0]);
			fprintf(stderr, "Example: %s 1000000\n", argv[0]);
			return 1;
		}
		num_elements = (size_t)parsed;
	}

	LOG(INFO, "=== Btree uint64_t Performance Benchmark (New API) ===");
	LOG(INFO, "Configuration:");
	LOG(INFO, "  Elements: %zu", num_elements);
	LOG(INFO, "  Searches per iteration: %d (1M)", SEARCHES_PER_ITER);
	LOG(INFO, "  Iterations: %d", NUM_ITERATIONS);
	LOG(INFO, "  Arena size: %llu MB", ARENA_SIZE / (1024 * 1024));
	LOG(INFO, "");

	// Run btree_u64 lower_bounds benchmark
	benchmark_btree_u64(num_elements);

	// Run btree_u64 upper_bounds benchmark
	benchmark_btree_u64_upper_bounds(num_elements);

	LOG(INFO, "=== Benchmark Complete ===");

	return 0;
}