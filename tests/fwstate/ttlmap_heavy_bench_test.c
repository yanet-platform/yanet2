#include "common/ttlmap/ttlmap.h"
#include "test_utils.h"
#include <pthread.h>
#include <stdalign.h>
#include <stdlib.h>

#define NUM_REPETITIONS 10
#define NUM_THREADS 10
#define L3_CACHE_SIZE (32ULL * 1024 * 1024) // 32MB typical L3 cache
#define VALUE_SIZE 64			    // B per value

#define MT_ARENA_SIZE (2 << 20) * 1024ULL * 1	      // MB arena for MT test
#define TOTAL_VALUES (L3_CACHE_SIZE / VALUE_SIZE * 8) // Nx L3 cache size
#define TOTAL_OPS (TOTAL_VALUES * NUM_THREADS * NUM_REPETITIONS)

#define TTL 50000

typedef int test_key_t;

typedef struct {
	uint8_t data[VALUE_SIZE];
} test_value_t;

typedef struct {
	ttlmap_t *map;
	uint16_t thread_id;
	int value_seed;
	double elapsed_time;
	uint64_t write_checksum;
	uint64_t read_checksum;
	int successful_writes;
	int successful_reads;
} mt_thread_data_t;

static void *
writer_thread(void *arg) {
	mt_thread_data_t *data = (mt_thread_data_t *)arg;

	data->write_checksum = 0;
	double start_time = get_time();
	int successful = 0;

	for (size_t j = 0; j < NUM_REPETITIONS; j++) {
		// Advance time on each iteration to expire old entries
		uint64_t current_time = j * 10000;

		for (size_t i = 0; i < TOTAL_VALUES; i++) {
			test_key_t key = (int)i;
			size_t id = key % NUM_THREADS;

			test_value_t *value = NULL;
			ttlmap_lock_t *lock = NULL;
			int res = TTLMAP_GET(
				data->map,
				&key,
				&value,
				&lock,
				current_time,
				TTL
			);

			if (TTLMAP_STATUS(res) != TTLMAP_FAILED) {
				memset(value->data, data->value_seed, VALUE_SIZE
				);
				value->data[id] = (uint8_t)id;

				ttlmap_release_lock(lock);

				successful++;
				if (j == 0 && id == data->thread_id) {
					data->write_checksum +=
						key + id + data->value_seed;
				}
			}
		}
	}

	double end_time = get_time();
	data->elapsed_time = end_time - start_time;
	data->successful_writes = successful;

	return NULL;
}

static void *
reader_thread_benchmark(void *arg) {
	mt_thread_data_t *data = (mt_thread_data_t *)arg;

	data->read_checksum = 0;
	double start_time = get_time();
	int successful = 0;

	for (int j = 0; j < NUM_REPETITIONS; j++) {
		uint64_t current_time = j * 10000;

		for (size_t i = 0; i < TOTAL_VALUES; i++) {
			test_key_t key = i;

			test_value_t value;
			int res = TTLMAP_LOOKUP(
				data->map, &key, &value, current_time
			);

			if (TTLMAP_STATUS(res) == TTLMAP_FOUND) {
				if (j == 0) {
					size_t id = key % NUM_THREADS;
					if (id == data->thread_id) {
						data->read_checksum +=
							key +
							value.data
								[data->thread_id] +
							data->value_seed;
					}
				}
				successful++;
			}
		}
	}

	double end_time = get_time();
	data->elapsed_time = end_time - start_time;
	data->successful_reads = successful;
	return NULL;
}

void
test_multithreaded_benchmark(void *mt_arena) {
	printf("Configuration:\n");
	printf("  Threads: %d\n", NUM_THREADS);
	printf("  Arena size: %s\n", numfmt(MT_ARENA_SIZE));
	printf("  Total values: %s\n", numfmt(TOTAL_VALUES));
	printf("  Value size: %d bytes\n", VALUE_SIZE);
	printf("  Total data size: %.2f MB (%.1fx L3 cache)\n",
	       (double)(TOTAL_VALUES * VALUE_SIZE) / (1024 * 1024),
	       (double)(TOTAL_VALUES * VALUE_SIZE) / L3_CACHE_SIZE);
	printf("\n");

	struct memory_context *ctx =
		init_context_from_arena(mt_arena, MT_ARENA_SIZE, "benchmark");

	ttlmap_t map;
	int res =
		TTLMAP_INIT(&map, ctx, test_key_t, test_value_t, TOTAL_VALUES);
	if (res != 0) {
		printf("Failed to create TTLMap (error=%d)\n", res);
		free_arena(mt_arena, MT_ARENA_SIZE);
		assert(false);
		exit(1);
	}

	uint8_t value_seed = (uint8_t)rand();

	pthread_t threads[NUM_THREADS];
	mt_thread_data_t thread_data[NUM_THREADS];

	double write_start = get_time();

	for (int i = 0; i < NUM_THREADS; i++) {
		thread_data[i].map = &map;
		thread_data[i].thread_id = i;
		thread_data[i].value_seed = value_seed;
		if (pthread_create(
			    &threads[i], NULL, writer_thread, &thread_data[i]
		    ) != 0) {
			printf("Failed to create writer thread %d\n", i);
			assert(false);
			exit(1);
		}
	}

	for (int i = 0; i < NUM_THREADS; i++) {
		pthread_join(threads[i], NULL);
	}

	double write_end = get_time();
	double total_write_time = write_end - write_start;
	double total_write_elapsed_time = 0.0;

	uint64_t total_successful_writes = 0;
	for (int i = 0; i < NUM_THREADS; i++) {
		total_successful_writes += thread_data[i].successful_writes;
		total_write_elapsed_time += thread_data[i].elapsed_time;
	}

	printf("\n%s%s+ Write Phase Results +%s\n", C_BOLD, C_YELLOW, C_RESET);
	printf("Wall time: %.3f seconds\n", total_write_time);
	printf("Thread time (sum): %.3f seconds\n", total_write_elapsed_time);
	printf("Total operations: %s\n", numfmt(TOTAL_OPS));
	printf("%sWrite throughput%s: %s ops/sec\n",
	       C_CYAN,
	       C_RESET,
	       numfmt(TOTAL_OPS / total_write_elapsed_time));

	printf("\nMap statistics after writes:\n");
	printf("  Memory used: %.2f MB\n",
	       map.mctx.balloc_size / (1024.0 * 1024.0));

	double read_start = get_time();
	for (int i = 0; i < NUM_THREADS; i++) {
		thread_data[i].map = &map;
		thread_data[i].thread_id = i;
		thread_data[i].value_seed = value_seed;

		if (pthread_create(
			    &threads[i],
			    NULL,
			    reader_thread_benchmark,
			    &thread_data[i]
		    ) != 0) {
			printf("Failed to create reader thread %d\n", i);
			assert(false);
			exit(1);
		}
	}

	for (int i = 0; i < NUM_THREADS; i++) {
		pthread_join(threads[i], NULL);
	}

	double read_end = get_time();
	double total_read_time = read_end - read_start;

	uint64_t total_successful_reads = 0;
	double total_read_elapsed_time = 0.0;
	for (int i = 0; i < NUM_THREADS; i++) {
		total_successful_reads += thread_data[i].successful_reads;
		total_read_elapsed_time += thread_data[i].elapsed_time;
	}

	printf("\n%s%s+ Read Phase Results +%s\n", C_BOLD, C_YELLOW, C_RESET);
	printf("Wall time: %.3f seconds\n", total_read_time);
	printf("Thread time (sum): %.3f seconds\n", total_read_elapsed_time);
	printf("Total operations: %s\n", numfmt(TOTAL_OPS));
	printf("%sRead throughput%s: %s ops/sec\n",
	       C_CYAN,
	       C_RESET,
	       numfmt(TOTAL_OPS / total_read_elapsed_time));

	printf("\n%s%s=== Overall Summary ===%s\n", C_BOLD, C_MAGENTA, C_RESET);
	printf("Total operations (write + read): %s\n", numfmt(TOTAL_OPS * 2));
	printf("Total successful operations: %s\n",
	       numfmt(total_successful_writes + total_successful_reads));
	for (int i = 0; i < NUM_THREADS; i++) {
		if (thread_data[i].read_checksum !=
		    thread_data[i].write_checksum) {
			printf("L%d: Read checksum mismatch for %d: "
			       "read=%zu "
			       "!= write=%zu\n",
			       __LINE__,
			       i,
			       thread_data[i].read_checksum,
			       thread_data[i].write_checksum);
			assert(false);
			exit(1);
		}
	}

	TTLMAP_FREE(&map);
	verify_memory_leaks(&map.mctx, "benchmark");

	printf("\n%s%sMulti-threaded benchmark test PASSED%s\n",
	       C_BLUE,
	       C_GREEN,
	       C_RESET);
}

int
main(void) {
	void *arena = allocate_hugepages_memory(MT_ARENA_SIZE);
	if (arena == NULL) {
		printf("Failed to allocate MT arena\n");
		assert(false);
		return -1;
	}

	printf("%s%s=== TTLMap Multi-threaded Benchmark Test ===%s\n\n",
	       C_BOLD,
	       C_GREEN,
	       C_RESET);

	test_multithreaded_benchmark(arena);

	free_arena(arena, MT_ARENA_SIZE);
	printf("\n%s%s=== All tests PASSED ===%s\n", C_BOLD, C_GREEN, C_RESET);
	return 0;
}
