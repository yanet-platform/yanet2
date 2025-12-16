#include "common/hugepages.h"
#include "common/memory.h"
#include "lib/fwstate/fwmap.h"
#include "test_utils.h"
#include <assert.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

// Multi-threaded test configuration
#define NUM_REPETITIONS 10
#define NUM_THREADS 10
#define L3_CACHE_SIZE (32ULL * 1024 * 1024) // 32MB typical L3 cache
#define VALUE_SIZE 64			    // B per value

#define MT_ARENA_SIZE (1 << 20) * 1024ULL * 1	      // MB arena for MT test
#define TOTAL_VALUES (L3_CACHE_SIZE / VALUE_SIZE * 8) // Nx L3 cache size
#define TOTAL_OPS (TOTAL_VALUES * NUM_THREADS * NUM_REPETITIONS)

volatile uint64_t now = 0; // Acceptable for testing purposes
const uint64_t ttl = 50000;

/* Custom key comparison for int keys */
static bool
bench_key_equal(const void *a, const void *b, size_t size) {
	(void)size;
	return *(const int *)a == *(const int *)b;
}

/* Custom key copy for int keys */
static void
bench_copy_key(void *dst, const void *src, size_t size) {
	(void)size;
	*(int *)dst = *(const int *)src;
}

/* Optimized value copy - treat as uint64_t array */
static void
bench_copy_value(void *dst, const void *src, size_t size) {
	uint64_t *d = (uint64_t *)dst;
	const uint64_t *s = (const uint64_t *)src;
	size_t count = size / sizeof(uint64_t);

	for (size_t i = 0; i < count; i++) {
		d[i] = s[i];
	}

	/* Handle remaining bytes with memcpy */
	size_t remaining = size % sizeof(uint64_t);
	if (remaining) {
		void *d_rem = (uint8_t *)dst + (count * sizeof(uint64_t));
		const void *s_rem =
			(const uint8_t *)src + (count * sizeof(uint64_t));
		memcpy(d_rem, s_rem, remaining);
	}
}

static void *
allocate_hugepages_memory(size_t size) {
	char *storage_path = "/dev/hugepages/arena";
	int mem_fd =
		open(storage_path, O_CREAT | O_TRUNC | O_RDWR, S_IRUSR | S_IWUSR
		);
	if (mem_fd < 0) {
		printf("L%d: failed to open storage path\n", __LINE__);
		return NULL;
	}

	if (ftruncate(mem_fd, size)) {
		printf("L%d: failed to truncate storage path\n", __LINE__);
		close(mem_fd);
		return NULL;
	}

	void *storage =
		mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_SHARED, mem_fd, 0);

	if (storage == MAP_FAILED) {
		int err = errno;
		printf("L%d: failed to create memory-mapped storage %s: %s\n",
		       __LINE__,
		       storage_path,
		       strerror(errno));

		if (err == ENOMEM && is_file_on_hugepages_fs(mem_fd) == 1) {
			printf("L%d: "
			       "the storage %s is meant to be allocated on "
			       "HUGETLBFS, but there is no memory. Maybe "
			       "because "
			       "either there are no preallocated pages or "
			       "another "
			       "process have consumed the memory\n",
			       __LINE__,
			       storage_path);
		}

		close(mem_fd);
		return NULL;
	}
	close(mem_fd);
	return storage;
}

// ============================================================================
// Multi-threaded Test Functions
// ============================================================================

// Thread data structure for MT tests
typedef struct {
	fwmap_t *map;
	uint16_t thread_id;
	int value_seed;
	double elapsed_time;
	// Per-thread counters
	uint64_t write_checksum;
	uint64_t read_checksum;
	int successful_writes;
	int successful_reads;
} mt_thread_data_t;

/**
 * Thread function for concurrent writes.
 */
static void *
writer_thread(void *arg) {
	mt_thread_data_t *data = (mt_thread_data_t *)arg;

	data->write_checksum = 0;
	double start_time = get_time();
	int successful = 0;

	size_t j = 0;
	for (; j < NUM_REPETITIONS; j++) {
		for (size_t i = 0; i < TOTAL_VALUES; i++) {
			int key = (int)i;
			size_t id = key % NUM_THREADS;

			/* Use entry API for zero-copy writes */
			rwlock_t *lock = NULL;
			fwmap_entry_t entry = fwmap_entry(
				data->map,
				data->thread_id,
				now,
				ttl,
				&key,
				&lock
			);

			if (entry.key) {
				/* Write directly to entry memory */
				if (entry.empty) {
					*(int *)entry.key = key;
				}
				memset(entry.value, data->value_seed, VALUE_SIZE
				);
				((uint8_t *)entry.value)[id] = (uint8_t)id;

				if (lock) {
					rwlock_write_unlock(lock);
				}

				successful++;
				if (j == 0 && id == data->thread_id) {
					data->write_checksum +=
						key + id + data->value_seed;
				}
			} else {
				printf("L%d ERROR: failed to get entry for "
				       "%d\n",
				       __LINE__,
				       key);
				if (errno) {
					perror("Reason: ");
				}
				assert(false);
				exit(1);
			}
		}
	}

	double end_time = get_time();
	data->elapsed_time = end_time - start_time;
	data->successful_writes = successful;

	return NULL;
}

/**
 * Thread function for concurrent reads in benchmark.
 */
static void *
reader_thread_benchmark(void *arg) {
	mt_thread_data_t *data = (mt_thread_data_t *)arg;

	data->read_checksum = 0;
	double start_time = get_time();
	int successful = 0;

	int j = 0;
	for (; j < NUM_REPETITIONS; j++) {
		for (size_t i = 0; i < TOTAL_VALUES; i++) {
			int key = i;

			rwlock_t *lock = NULL;
			uint8_t *value;
			int ret = fwmap_get(
				data->map, now, &key, (void **)&value, &lock
			);
			if (ret >= 0) {
				if (j == 0) {
					size_t id = key % NUM_THREADS;
					if (id == data->thread_id) {
						data->read_checksum +=
							key +
							value[data->thread_id] +
							data->value_seed;
					}
				}
				rwlock_read_unlock(lock);
				successful++;
			} else {
				printf("L%d ERROR: value with key=%d is not "
				       "found\n",
				       __LINE__,
				       key);
				assert(false);
				exit(1);
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
	size_t index_size = TOTAL_VALUES;

	printf("Configuration:\n");
	printf("  Threads: %d\n", NUM_THREADS);
	printf("  Arena size: %s\n", numfmt(MT_ARENA_SIZE));
	printf("  Total values: %s\n", numfmt(TOTAL_VALUES));
	printf("  Index size: %s\n", numfmt(index_size));
	printf("  Value size: %d bytes\n", VALUE_SIZE);
	printf("  Total data size: %.2f MB (%.1fx L3 cache)\n",
	       (double)(TOTAL_VALUES * VALUE_SIZE) / (1024 * 1024),
	       (double)(TOTAL_VALUES * VALUE_SIZE) / L3_CACHE_SIZE);
	printf("  Map index size (%s bytes): %s\n",
	       numfmt(index_size * 8),
	       numfmt(index_size));
	printf("\n");

	struct memory_context *ctx =
		init_context_from_arena(mt_arena, MT_ARENA_SIZE, "benchmark");

	/* Register custom functions in registry */
	static bool registered = false;
	if (!registered) {
		fwmap_func_registry[FWMAP_KEY_EQUAL_DEFAULT] =
			(void *)bench_key_equal;
		fwmap_func_registry[FWMAP_COPY_KEY_DEFAULT] =
			(void *)bench_copy_key;
		fwmap_func_registry[FWMAP_COPY_VALUE_DEFAULT] =
			(void *)bench_copy_value;
		registered = true;
	}

	fwmap_config_t config = {
		.key_size = sizeof(int),
		.value_size = VALUE_SIZE,
		.hash_seed = 0,
		.worker_count = NUM_THREADS,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_DEFAULT,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_DEFAULT,
		.copy_value_fn_id = FWMAP_COPY_VALUE_DEFAULT,
		.index_size = index_size,
		.extra_bucket_count = index_size >> 8,
	};

	// Create map with appropriate size
	fwmap_t *map = fwmap_new(&config, ctx);
	if (!map) {
		if (errno != 0) {
			perror("failed to create FWMap: ");
		} else {

			printf("Failed to create FWMap (unknown error)\n");
		}
		free_arena(mt_arena, MT_ARENA_SIZE);
		assert(false);
		exit(1);
	}

	uint8_t value_seed = (uint8_t)rand();

	// Create thread data
	pthread_t threads[NUM_THREADS];
	mt_thread_data_t thread_data[NUM_THREADS];

	// Phase 1: Concurrent writes
	double write_start = get_time();

	for (int i = 0; i < NUM_THREADS; i++) {
		thread_data[i].map = map;
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

	// Wait for all writer threads to complete
	for (int i = 0; i < NUM_THREADS; i++) {
		pthread_join(threads[i], NULL);
	}

	double write_end = get_time();
	double total_write_time = write_end - write_start;
	double total_write_elapsed_time = 0.0;

	// Sum up per-thread write statistics (fast, no atomics)
	uint64_t total_successful_writes = 0;
	for (int i = 0; i < NUM_THREADS; i++) {
		total_successful_writes += thread_data[i].successful_writes;
		total_write_elapsed_time += thread_data[i].elapsed_time;
	}

	printf("\n"
	       "%s%s+ Write Phase Results +%s\n",
	       C_BOLD,
	       C_YELLOW,
	       C_RESET);
	printf("Wall write time: %.3f seconds\n", total_write_time);
	printf("Total write time (CPU time): %.3f seconds\n",
	       total_write_elapsed_time);
	printf("Total write operations: %s\n", numfmt(TOTAL_OPS));
	printf("Successful writes: %s\n", numfmt(total_successful_writes));
	printf("%sWrite throughput%s: %s ops/sec\n",
	       C_CYAN,
	       C_RESET,
	       numfmt(TOTAL_OPS / total_write_elapsed_time));
	assert(TOTAL_OPS == total_successful_writes);

	// Get map statistics
	fwmap_stats_t stats = fwmap_get_stats(map);
	printf("\nMap statistics after writes:\n");
	printf("  Total elements: %s\n", numfmt(stats.total_elements));
	printf("  Max chain length: %u\n", stats.max_chain_length);
	printf("  Memory used: %.2f MB\n",
	       stats.memory_used / (1024.0 * 1024.0));

	// Phase 2: Concurrent reads
	double read_start = get_time();
	for (int i = 0; i < NUM_THREADS; i++) {
		thread_data[i].map = map;
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

	// Wait for all reader threads to complete
	for (int i = 0; i < NUM_THREADS; i++) {
		pthread_join(threads[i], NULL);
	}

	double read_end = get_time();
	double total_read_time = read_end - read_start;

	// Sum up per-thread read statistics (fast, no atomics)
	uint64_t total_successful_reads = 0;
	double total_read_elapsed_time = 0.0;
	uint64_t result_read_checksum = 0;
	for (int i = 0; i < NUM_THREADS; i++) {
		total_successful_reads += thread_data[i].successful_reads;
		total_read_elapsed_time += thread_data[i].elapsed_time;
		result_read_checksum += thread_data[i].read_checksum;
	}
	printf("\n%s%s+ Read Phase Results +%s\n", C_BOLD, C_YELLOW, C_RESET);
	printf("Wall read time: %.3f seconds\n", total_read_time);
	printf("Total read time (CPU time): %.3f seconds\n",
	       total_read_elapsed_time);
	printf("Total read operations: %s\n", numfmt(TOTAL_OPS));
	printf("Successful reads: %s\n", numfmt(total_successful_reads));
	printf("Read checksum: %zu\n", result_read_checksum);
	printf("%sRead throughput:%s %s ops/sec\n",
	       C_CYAN,
	       C_RESET,
	       numfmt(TOTAL_OPS / total_read_elapsed_time));

	// Overall summary
	printf("\n%s%s=== Overall Summary ===%s\n", C_BOLD, C_MAGENTA, C_RESET);
	printf("Main arena size %llu MB\n", MT_ARENA_SIZE >> 20);
	printf("Total operations (write + read): %s\n", numfmt(TOTAL_OPS * 2));
	printf("Total successful operations: %s\n",
	       numfmt(total_successful_writes + total_successful_reads));

	// Verify 100% success rate by comparing actual counts (avoids
	// floating-point precision issues)
	if (total_successful_writes != TOTAL_OPS) {
		printf("L%d ERROR: Write success rate (%lu/%llu) is below "
		       "required threshold\n",
		       __LINE__,
		       total_successful_writes,
		       TOTAL_OPS);
		assert(false);
		exit(1);
	}
	if (total_successful_reads != TOTAL_OPS) {
		printf("L%d ERROR: Read success rate (%lu/%llu) is below "
		       "required "
		       "threshold\n",
		       __LINE__,
		       total_successful_reads,
		       TOTAL_OPS);
		assert(false);
		exit(1);
	}
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

	// Cleanup
	fwmap_destroy(map, ctx);

	printf("\n%s%sMulti-threaded benchmark test PASSED%s\n",
	       C_BLUE,
	       C_GREEN,
	       C_RESET);
}

int
main() {
	// Create common arena for all tests
	void *arena = allocate_hugepages_memory(MT_ARENA_SIZE);
	if (arena == NULL) {
		printf("Failed to allocate MT arena\n");
		assert(false);
		return -1;
	}

	printf("%s%s=== Multi-threaded Benchmark Test ===%s\n\n",
	       C_BOLD,
	       C_GREEN,
	       C_RESET);

	test_multithreaded_benchmark(arena);

	free_arena(arena, MT_ARENA_SIZE);
	printf("\n%s%s=== All tests PASSED ===%s\n", C_BOLD, C_GREEN, C_RESET);
	return 0;
}
