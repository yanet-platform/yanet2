#include "common/memory.h"
#include "lib/fwstate/fwmap.h"
#include "test_utils.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/time.h>
#include <unistd.h>

#define ARENA_SIZE (1 << 20) * 1024 // MB arena

#define NUM_REPETITIONS 10
#define L3_CACHE_SIZE (32ULL * 1024 * 1024) // 32MB typical L3 cache

volatile uint64_t now = 0; // For testing purposes it's fine.
const uint64_t ttl = 50000;

void
benchmark_performance(void *arena) {
	printf("\nPerformance benchmark:\n");
	size_t worker_idx = 0;

	// Create fresh memory context from common arena.
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "benchmark");

	const int index_size = L3_CACHE_SIZE / sizeof(int) / 2;
	fwmap_config_t config = {
		.key_size = sizeof(int),
		.value_size = sizeof(int),
		.hash_seed = 0,
		.worker_count = 1,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_DEFAULT,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.index_size = index_size,
		.extra_bucket_count = index_size >> 8,
	};

	fwmap_t *map = fwmap_new(&config, ctx);
	assert(map != NULL);

	// Benchmark insertions.
	double start = get_time();
	for (int j = 0; j < NUM_REPETITIONS; j++) {
		for (int i = 0; i < index_size; i++) {
			int key = i;
			int value = i;
			int ret = fwmap_put(
				map, worker_idx, now, ttl, &key, &value, NULL
			);
			if (ret < 0) {
				printf("L%d: failed to insert key %d on "
				       "repetition %d\n",
				       __LINE__,
				       i,
				       j + 1);
				assert(false);
				exit(1);
			}
		}
	}
	double end = get_time();
	size_t total_elements = map->counters[0].total_elements;
	assert(total_elements == (size_t)index_size);
	double insert_time = (end - start) / (double)NUM_REPETITIONS;
	double insert_throughput = index_size / insert_time;
	printf("  Inserted %s items in %.3f seconds %s(%s ops/sec)%s\n",
	       numfmt(index_size),
	       insert_time,
	       C_CYAN,
	       numfmt((size_t)insert_throughput),
	       C_RESET);

	// Benchmark lookups.
	start = get_time();
	volatile uint64_t checksum = 0; // Prevent compiler optimization.
	for (int j = 0; j < NUM_REPETITIONS; j++) {
		for (int i = 0; i < index_size; i++) {
			int key = i;
			int *value;
			int get_ok = fwmap_get(
				map, now, &key, (void **)&value, NULL
			);
			if (get_ok < 0) {
				printf("failed to get key: %d at L%d",
				       key,
				       __LINE__);
				assert(false);
				exit(1);
			}
			if (j == 0) {

				checksum +=
					*value; // Force compiler to actually
						// use the read value.
			}
		}
	}
	end = get_time();
	// Use checksum to prevent dead code elimination.
	if (checksum == 0) {
		printf("Unexpected checksum\n");
		assert(false);
		exit(1);
	}
	double lookup_time = ((double)(end - start)) / (double)NUM_REPETITIONS;
	double lookup_throughput = index_size / lookup_time;
	printf("  Looked up %s items in %.3f seconds %s(%s ops/sec)%s\n",
	       numfmt(index_size),
	       lookup_time,
	       C_CYAN,
	       numfmt((size_t)lookup_throughput),
	       C_RESET);

	// Print statistics.
	fwmap_stats_t stats = fwmap_get_stats(map);
	printf("  Final statistics:\n");
	printf("    Total elements: %s\n", numfmt(stats.total_elements));
	printf("    Index size: %u\n", stats.index_size);
	printf("    Max chain length: %u\n", stats.max_chain_length);
	printf("    Memory used: %zu KB\n", stats.memory_used / 1024);

	fwmap_destroy(map, ctx);

	// Verify memory leaks.
	verify_memory_leaks(ctx, "benchmark_performance");
}

int
main() {
	printf("%s%s=== FWMap Single Threaded Benchmark ===%s\n\n",
	       C_BOLD,
	       C_WHITE,
	       C_RESET);

	// Create common arena for all tests
	void *arena = allocate_locked_memory(ARENA_SIZE);
	if (arena == NULL) {
		printf("could not allocate arena\n");
		return -1;
	}

	printf("%s%s=== Single-threaded Tests ===%s\n", C_BOLD, C_BLUE, C_RESET
	);
	benchmark_performance(arena);

	free_arena(arena, ARENA_SIZE);
	printf("\n%s%s=== All tests PASSED ===%s\n", C_BOLD, C_GREEN, C_RESET);
	return 0;
}
