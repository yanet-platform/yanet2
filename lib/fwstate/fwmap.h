#pragma once
#include <assert.h>
#include <errno.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>
#include <sys/random.h>
#include <sys/types.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"
#include "common/numutils.h"
#include "common/rwlock.h"

// Per user includes
#include "ops.h"

// ============================================================================
// Constants and Global Registry
// ============================================================================

#define FWMAP_BUCKET_ENTRIES 4
#define FWMAP_BUCKET_SIZE 64
#define FWMAP_CHUNK_INDEX_MAX_SIZE                                             \
	(MEMORY_BLOCK_ALLOCATOR_MAX_SIZE / FWMAP_BUCKET_SIZE)
#define FWMAP_CHUNK_INDEX_MASK (FWMAP_CHUNK_INDEX_MAX_SIZE - 1)

// Function registry for cross-process compatibility.
// NOLINTBEGIN(readability-identifier-naming)
typedef enum {
	FWMAP_UNINITIALIZED = 0,
	FWMAP_HASH_FNV1A,
	FWMAP_KEY_EQUAL_DEFAULT,
	FWMAP_RAND_DEFAULT,
	FWMAP_RAND_SECURE,
	FWMAP_COPY_KEY_DEFAULT,
	FWMAP_COPY_VALUE_DEFAULT,
	FWMAP_MERGE_VALUE_DEFAULT,
	FWMAP_COPY_KEY_FW4,
	FWMAP_COPY_KEY_FW6,
	FWMAP_COPY_VALUE_FWSTATE,
	FWMAP_MERGE_VALUE_FWSTATE,
	FWMAP_KEY_EQUAL_FW4,
	FWMAP_KEY_EQUAL_FW6,
	FWMAP_FUNC_COUNT
} fwmap_func_id_t;
// NOLINTEND(readability-identifier-naming)

static_assert(FWMAP_FUNC_COUNT < 255, "Too many functions");

// Global function registry (declared here, defined at the bottom).
static void *fwmap_func_registry[FWMAP_FUNC_COUNT];

// Hash function type.
typedef uint64_t (*fwmap_hash_fn_t)(
	const void *key, size_t key_size, uint32_t seed
);

// Key comparison function type.
typedef bool (*fwmap_key_equal_fn_t)(
	const void *key1, const void *key2, size_t key_size
);

// Random number generator for hash seed randomization.
// Prevents hash collision attacks and ensures different distributions.
typedef uint64_t (*fwmap_rand_fn_t)(void);

// Copy function types for custom key/value copying.
typedef void (*fwmap_copy_key_fn_t)(void *dst, const void *src, size_t size);
typedef void (*fwmap_copy_value_fn_t)(void *dst, const void *src, size_t size);
typedef void (*fwmap_merge_value_fn_t)(
	void *dst, const void *new_value, const void *old_value, size_t size
);

typedef struct fwmap_config {
	uint16_t key_size;
	uint16_t value_size;
	uint32_t hash_seed;
	uint16_t worker_count;
	uint32_t index_size;
	uint32_t extra_bucket_count;
	fwmap_func_id_t hash_fn_id;
	fwmap_func_id_t key_equal_fn_id;
	fwmap_func_id_t rand_fn_id;
	fwmap_func_id_t copy_key_fn_id;
	fwmap_func_id_t copy_value_fn_id;
	fwmap_func_id_t merge_value_fn_id;
} fwmap_config_t;

typedef struct fwmap_bucket {
	rwlock_t lock;
	uint32_t next;
	uint16_t sig[FWMAP_BUCKET_ENTRIES];
	uint64_t deadline[FWMAP_BUCKET_ENTRIES];
	uint32_t idx[FWMAP_BUCKET_ENTRIES];
} __attribute__((__aligned__(64))) fwmap_bucket_t;

typedef struct fwmap_counter {
	uint32_t sealed;
	uint32_t max_chain;
	uint32_t total_elements;
	uint64_t max_deadline;
	uint64_t padding[5];
} __attribute__((__aligned__(64))) fwmap_counter_t;
//} fwmap_counter_t;

typedef struct fwmap {
	fwmap_bucket_t **buckets;
	fwmap_bucket_t *extra_buckets;
	uint8_t **key_store;
	uint8_t **value_store;

	uint32_t extra_size;
	uint32_t index_mask;

	uint16_t key_size;
	uint16_t value_size;
	uint16_t worker_count;
	uint16_t buckets_chunk_shift;

	uint32_t hash_seed;

	uint32_t keys_in_chunk;
	uint32_t values_in_chunk;

	// Indices into the function registry
	uint8_t hash_fn_id;
	uint8_t key_equal_fn_id;
	uint8_t copy_key_fn_id;
	uint8_t copy_value_fn_id;
	uint8_t merge_value_fn_id;

	// Alignment offsets for cache-aligned allocations
	uint8_t map_alloc_offset;
	uint8_t buckets_alloc_offset;
	uint8_t extra_buckets_alloc_offset;
	uint8_t _padding;

	uint32_t key_cursor;
	uint32_t extra_free_idx;

	uint32_t keys_chunk_cnt;
	uint32_t values_chunk_cnt;

	// Number of workers that have seen this map as sealed
	uint32_t sealed_count;

	uint64_t max_deadline;
	volatile struct fwmap *next;
	uint64_t padding[2];
	fwmap_counter_t counters[];
} __attribute__((__aligned__(64))) fwmap_t;

typedef struct fwmap_stats {
	uint32_t total_elements;
	uint32_t index_size;
	uint32_t extra_bucket_count;
	uint32_t max_chain_length;
	uint64_t max_deadline;
	size_t memory_used;
} fwmap_stats_t;

typedef struct fwmap_entry {
	void *key;
	void *value;
	uint32_t idx;
	bool empty;
} fwmap_entry_t;

// ============================================================================
// Default Functions
// ============================================================================

// Default FNV-1a hash function with loop unrolling.
static inline uint64_t
fwmap_hash_fnv1a(const void *key, size_t key_size, uint32_t seed) {
	const uint8_t *data = (const uint8_t *)key;
	uint64_t hash = 14695981039346656037ULL ^ (uint64_t)seed;
	const uint64_t fnv_prime = 1099511628211ULL;

	// Process 4 bytes at a time
	size_t i = 0;
	size_t unroll_limit = key_size & ~3ULL; // Round down to multiple of 4

	for (; i < unroll_limit; i += 4) {
		hash ^= data[i];
		hash *= fnv_prime;
		hash ^= data[i + 1];
		hash *= fnv_prime;
		hash ^= data[i + 2];
		hash *= fnv_prime;
		hash ^= data[i + 3];
		hash *= fnv_prime;
	}

	// Process remaining bytes (0-3 bytes)
	switch (key_size & 3) {
	case 3:
		hash ^= data[i + 2];
		hash *= fnv_prime;
		// fallthrough
	case 2:
		hash ^= data[i + 1];
		hash *= fnv_prime;
		// fallthrough
	case 1:
		hash ^= data[i];
		hash *= fnv_prime;
		// fallthrough
	case 0:
		break;
	}

	return hash;
}

// This global state is used only during fwmap_new, so there should be no
// contention.
static uint64_t fwmap_rand_lcg_state = 1;

// Simple LCG for testing and general use.
static inline uint64_t
fwmap_rand_default(void) {
	fwmap_rand_lcg_state = fwmap_rand_lcg_state * 1103515245 + 12345;
	return fwmap_rand_lcg_state;
}

// Secure random using system entropy.
static inline uint64_t
fwmap_rand_secure(void) {
	uint32_t seed;
	int ret = getrandom(&seed, sizeof(seed), 0);
	(void)ret;
	return seed;
}

// Default key comparison function using memcmp.
static inline bool
fwmap_default_key_equal(const void *a, const void *b, size_t size) {
	switch (size) {
	case 4:
		return *(uint32_t *)a == *(uint32_t *)b;
	case 8:
		return *(uint64_t *)a == *(uint64_t *)b;
	default:
		return memcmp(a, b, size) == 0;
	}
}

// Default copy functions using memcpy.
static inline void
fwmap_default_copy_key(void *dst, const void *src, size_t size) {
	memcpy(dst, src, size);
}

static inline void
fwmap_default_copy_value(void *dst, const void *src, size_t size) {
	memcpy(dst, src, size);
}

static inline void
fwmap_default_merge_value(
	void *dst, const void *old_value, const void *new_value, size_t size
) {
	(void)dst, (void)old_value, (void)new_value, (void)size;
	// nop
	return;
}

// Helper function to set default function IDs for uninitialized fields
static inline void
fwmap_config_set_defaults(fwmap_config_t *config) {
	if (config->hash_fn_id == FWMAP_UNINITIALIZED) {
		config->hash_fn_id = FWMAP_HASH_FNV1A;
	}
	if (config->key_equal_fn_id == FWMAP_UNINITIALIZED) {
		config->key_equal_fn_id = FWMAP_KEY_EQUAL_DEFAULT;
	}
	if (config->rand_fn_id == FWMAP_UNINITIALIZED) {
		config->rand_fn_id = FWMAP_RAND_DEFAULT;
	}
	if (config->copy_key_fn_id == FWMAP_UNINITIALIZED) {
		config->copy_key_fn_id = FWMAP_COPY_KEY_DEFAULT;
	}
	if (config->copy_value_fn_id == FWMAP_UNINITIALIZED) {
		config->copy_value_fn_id = FWMAP_COPY_VALUE_DEFAULT;
	}
	if (config->merge_value_fn_id == FWMAP_UNINITIALIZED) {
		config->merge_value_fn_id = FWMAP_MERGE_VALUE_DEFAULT;
	}
}

// ============================================================================
// Utility Operations
// ============================================================================

// Helper function to allocate memory with 64-byte alignment
// Returns aligned pointer and stores offset in *offset_ptr for later free
static inline void *
fwmap_balloc_aligned(
	struct memory_context *ctx,
	size_t size,
	size_t alignment,
	uint8_t *offset_ptr
) {
	if (size + alignment >= MEMORY_BLOCK_ALLOCATOR_MAX_SIZE) {
		*offset_ptr = 0;
		return memory_balloc(ctx, size);
	}

	// Allocate extra space for alignment
	size_t alloc_size = size + alignment - 1;
	void *raw = memory_balloc(ctx, alloc_size);
	if (!raw) {
		return NULL;
	}

	// Calculate aligned address
	uintptr_t raw_addr = (uintptr_t)raw;
	uintptr_t aligned_addr = (raw_addr + alignment - 1) & ~(alignment - 1);

	// Store offset for later deallocation
	*offset_ptr = (uint8_t)(aligned_addr - raw_addr);

	return (void *)aligned_addr;
}

// Helper function to free memory allocated with fwmap_balloc_aligned
static inline void
fwmap_bfree_aligned(
	struct memory_context *ctx,
	void *aligned_ptr,
	size_t size,
	size_t alignment,
	uint8_t offset
) {
	if (!aligned_ptr) {
		return;
	}

	// Calculate original allocation address using stored offset
	void *raw = (void *)((uintptr_t)aligned_ptr - offset);

	// Calculate original allocation size
	size_t alloc_size =
		(size + alignment >= MEMORY_BLOCK_ALLOCATOR_MAX_SIZE)
			? size
			: size + alignment - 1;

	memory_bfree(ctx, raw, alloc_size);
}

static inline uint8_t *
fwmap_get_key(fwmap_t *map, uint32_t idx) {
	uint32_t chunk_idx = 0;
	// chunk_cnt is expected to be small
	while (idx >= map->keys_in_chunk) {
		idx -= map->keys_in_chunk;
		chunk_idx++;
	}

	uint8_t **key_store = ADDR_OF(&map->key_store);
	uint8_t *chunk = (uint8_t *)(ADDR_OF(&key_store[chunk_idx]));
	uint8_t *key_slot = chunk + (size_t)idx * map->key_size;
	return key_slot;
}

static inline uint8_t *
fwmap_get_value(fwmap_t *map, uint32_t idx) {

	uint32_t chunk_idx = 0;
	// chunk_cnt is expected to be small
	while (idx >= map->values_in_chunk) {
		idx -= map->values_in_chunk;
		chunk_idx++;
	}
	uint8_t **value_store = ADDR_OF(&map->value_store);
	uint8_t *chunk = (uint8_t *)(ADDR_OF(&value_store[chunk_idx]));
	uint8_t *value_slot = chunk + (size_t)idx * map->value_size;
	return value_slot;
}

static inline size_t
fwmap_size(const fwmap_t *map) {
	size_t total = 0;
	if (map) {
		for (size_t i = 0; i < map->worker_count; i++) {
			total += map->counters[i].total_elements;
		}
	}
	return total;
}

static inline bool
fwmap_empty(const fwmap_t *map) {
	if (map) {
		for (size_t i = 0; i < map->worker_count; i++) {
			if (map->counters[i].total_elements) {
				return false;
			}
		}
	}
	return true;
}

static inline size_t
fwmap_max_chain_length(const fwmap_t *map) {
	size_t chain = 0;
	if (map) {
		for (size_t i = 0; i < map->worker_count; i++) {
			if (chain < map->counters[i].max_chain) {
				chain = map->counters[i].max_chain;
			}
		}
	}
	return chain;
}

static inline uint64_t
fwmap_max_deadline(const fwmap_t *map) {
	uint64_t deadline = map->counters[0].max_deadline;
	for (size_t i = 1; i < map->worker_count; i++) {
		if (map->counters[i].max_deadline > deadline) {
			deadline = map->counters[i].max_deadline;
		}
	}
	return deadline;
}

static inline fwmap_stats_t
fwmap_get_stats(const fwmap_t *map) {
	fwmap_stats_t stats = {
		.total_elements = fwmap_size(map),
		.index_size = map->index_mask + 1,
		.extra_bucket_count = map->extra_size,
		.max_chain_length = fwmap_max_chain_length(map),
		.max_deadline = fwmap_max_deadline(map),
	};

	// Calculate memory usage
	size_t total_memory = 0;

	// 1. Main map structure with counters
	size_t map_size =
		sizeof(fwmap_t) + sizeof(fwmap_counter_t) * map->worker_count;
	total_memory += map_size;

	// 2. Bucket chunks array (array of pointers)
	uint32_t chunk_count =
		(map->index_mask >> map->buckets_chunk_shift) + 1;
	size_t chunks_array_size = sizeof(fwmap_bucket_t *) * chunk_count;
	total_memory += chunks_array_size;

	// 3. Bucket chunks (actual bucket storage)
	size_t index_chunk_size =
		sizeof(fwmap_bucket_t) *
		((map->index_mask & FWMAP_CHUNK_INDEX_MASK) + 1);
	total_memory += index_chunk_size * chunk_count;

	// 4. Extra buckets for chaining
	if (map->extra_size > 0) {
		size_t extra_buckets_size =
			sizeof(fwmap_bucket_t) * map->extra_size;
		total_memory += extra_buckets_size;
	}

	// 5. Key store array (array of pointers to key chunks)
	size_t key_store_array_size = sizeof(uint8_t *) * map->keys_chunk_cnt;
	total_memory += key_store_array_size;

	// 6. Key chunks (actual key storage)
	total_memory += (size_t)map->key_size * (map->index_mask + 1);

	// 7. Value store array (array of pointers to value chunks)
	size_t value_store_array_size =
		sizeof(uint8_t *) * map->values_chunk_cnt;
	total_memory += value_store_array_size;

	// 8. Value chunks (actual value storage)
	total_memory += (size_t)map->value_size * (map->index_mask + 1);

	stats.memory_used = total_memory;

	return stats;
}

static inline int
fwmap_allocate_chunks(
	struct memory_context *ctx,
	uint8_t **store,
	uint32_t size,
	uint32_t chunk_size,
	uint32_t chunks,
	uint32_t item_size
) {
	for (uint32_t i = 0; i < chunks; i++) {
		uint32_t keys = size > chunk_size ? chunk_size : size;

		size_t chunk_store_size = keys * item_size;
		uint8_t *chunk_store = memory_balloc(ctx, chunk_store_size);
		if (!chunk_store) {
			// Mark the stopping point for deallocation.
			store[i] = NULL;
			errno = ENOMEM;
			return -1;
		}
		memset(chunk_store, 0, chunk_store_size);
		SET_OFFSET_OF(&store[i], chunk_store);

		if (size <= chunk_size) {
			break; // Do not allocate more keys than index_size.
		}
		size -= chunk_size;
	}
	return 0;
}

static inline int64_t
fwmap_next_free_key(fwmap_t *map) {
	if (map->key_cursor > map->index_mask) {
		return -1;
	}
	uint32_t curr_key =
		__atomic_fetch_add(&map->key_cursor, 1, __ATOMIC_RELAXED);
	if (curr_key > map->index_mask) {
		return -1;
	}

	return curr_key;
}

// Utility function to update counters (max_chain, total_elements, and
// max_deadline).
static inline void
fwmap_update_counters(
	fwmap_t *map,
	uint16_t worker_idx,
	size_t chain_length,
	int increment_total,
	uint64_t deadline
) {
	fwmap_counter_t *counter = &map->counters[worker_idx];
	counter->total_elements += increment_total;
	if (chain_length > counter->max_chain) {
		counter->max_chain = chain_length;
	}
	if (deadline > counter->max_deadline) {
		counter->max_deadline = deadline;
	}
}

// ============================================================================
// Core Map Operations
// ============================================================================

// Free a FWMap and all its resources.
static inline void
fwmap_destroy(fwmap_t *map, struct memory_context *ctx) {
	if (!map) {
		return;
	}

	fwmap_bucket_t **chunks = ADDR_OF(&map->buckets);

	if (chunks) {
		size_t chunk_count =
			(map->index_mask >> map->buckets_chunk_shift) + 1;
		size_t chunk_size =
			sizeof(fwmap_bucket_t) *
			((map->index_mask & FWMAP_CHUNK_INDEX_MASK) + 1);

		for (size_t i = 0; i < chunk_count; i++) {
			// In case of allocation failure, the first null pointer
			// indicates the failed allocation.
			if (!chunks[i]) {
				break;
			}
			fwmap_bucket_t *buckets = ADDR_OF(&chunks[i]);
			// Free the bucket array with alignment offset
			fwmap_bfree_aligned(
				ctx,
				buckets,
				chunk_size,
				64,
				map->buckets_alloc_offset
			);
		}
		memory_bfree(
			ctx, chunks, sizeof(fwmap_bucket_t *) * chunk_count
		);
	}
	if (map->extra_buckets) {
		fwmap_bfree_aligned(
			ctx,
			ADDR_OF(&map->extra_buckets),
			sizeof(fwmap_bucket_t) * map->extra_size,
			64,
			map->extra_buckets_alloc_offset
		);
	}

	uint8_t **key_chunks = ADDR_OF(&map->key_store);
	if (key_chunks) {
		size_t key_chunk_size = map->keys_in_chunk * map->key_size;
		for (size_t i = 0; i < map->keys_chunk_cnt; i++) {
			// In case of allocation failure, the first null pointer
			// indicates the failed allocation.
			if (!key_chunks[i]) {
				break;
			}
			uint8_t *kchunk = ADDR_OF(&key_chunks[i]);
			memory_bfree(ctx, kchunk, key_chunk_size);
		}
		memory_bfree(
			ctx, key_chunks, sizeof(uint8_t *) * map->keys_chunk_cnt
		);
	}

	uint8_t **value_chunks = ADDR_OF(&map->value_store);
	if (value_chunks) {
		size_t value_chunk_size =
			map->values_in_chunk * map->value_size;
		for (size_t i = 0; i < map->values_chunk_cnt; i++) {
			// In case of allocation failure, the first null pointer
			// indicates the failed allocation.
			if (!value_chunks[i]) {
				break;
			}
			uint8_t *vchunk = ADDR_OF(&value_chunks[i]);
			memory_bfree(ctx, vchunk, value_chunk_size);
		}
		memory_bfree(
			ctx,
			value_chunks,
			sizeof(uint8_t *) * map->values_chunk_cnt
		);
	}

	size_t map_size =
		sizeof(fwmap_t) + sizeof(fwmap_counter_t) * map->worker_count;

	// Calculate original allocation address using stored offset
	void *raw_map = (void *)((uintptr_t)map - map->map_alloc_offset);
	size_t alloc_size = map_size + 63;
	memory_bfree(ctx, raw_map, alloc_size);
}

static inline fwmap_t *
fwmap_new(const fwmap_config_t *user_config, struct memory_context *ctx) {
	// Create a mutable copy of config to set defaults
	fwmap_config_t config = *user_config;
	fwmap_config_set_defaults(&config);

	uint32_t index_size = config.index_size;
	uint32_t extra_size = config.extra_bucket_count;

	if (index_size < 16) {
		index_size = 16;
	}
	// Ensure index_size is a power of 2.
	index_size = align_up_pow2(index_size);
	if (!index_size) {
		errno = EINVAL;
		return NULL;
	}

	if (extra_size) {
		if (extra_size > FWMAP_CHUNK_INDEX_MAX_SIZE) {
			errno = EINVAL;
			return NULL;
		}
		extra_size = align_up_pow2(extra_size);
	}

	uint32_t keys_per_chunk =
		MEMORY_BLOCK_ALLOCATOR_MAX_SIZE / config.key_size;
	// Ceiling division: (index_size + keys_per_chunk - 1) / keys_per_chunk.
	// But ensure at least 1 chunk even if keys_per_chunk is 0.
	uint32_t keys_chunk_cnt =
		(index_size + keys_per_chunk - 1) / keys_per_chunk;

	uint32_t values_per_chunk =
		MEMORY_BLOCK_ALLOCATOR_MAX_SIZE / config.value_size;
	// Ceiling division with minimum of 1.
	uint32_t values_chunk_cnt =
		(index_size + values_per_chunk - 1) / values_per_chunk;

	// Check for overflow.
	if (keys_per_chunk * keys_chunk_cnt < index_size ||
	    values_per_chunk * values_chunk_cnt < index_size) {
		errno = EINVAL;
		return NULL;
	}

	fwmap_rand_fn_t rand_fn =
		(fwmap_rand_fn_t)fwmap_func_registry[config.rand_fn_id];

	size_t map_size =
		sizeof(fwmap_t) + sizeof(fwmap_counter_t) * config.worker_count;

	// Allocate with extra space for 64-byte alignment
	size_t alloc_size = map_size + 63;
	void *raw_map = memory_balloc(ctx, alloc_size);
	if (!raw_map) {
		errno = ENOMEM;
		return NULL;
	}

	// Calculate 64-byte aligned address
	uintptr_t raw_addr = (uintptr_t)raw_map;
	uintptr_t aligned_addr = (raw_addr + 63) & ~(uintptr_t)63;
	fwmap_t *map = (fwmap_t *)aligned_addr;

	// Store offset for later deallocation
	uint8_t offset = (uint8_t)(aligned_addr - raw_addr);

	memset(map, 0, map_size);
	map->map_alloc_offset = offset;

	map->key_size = config.key_size;
	map->value_size = config.value_size;
	map->hash_seed =
		config.hash_seed ? config.hash_seed : (uint32_t)rand_fn();
	map->worker_count = config.worker_count;

	map->hash_fn_id = config.hash_fn_id;
	map->key_equal_fn_id = config.key_equal_fn_id;
	map->copy_key_fn_id = config.copy_key_fn_id;
	map->copy_value_fn_id = config.copy_value_fn_id;
	map->merge_value_fn_id = config.merge_value_fn_id;

	map->index_mask = index_size - 1;
	// Shift amount equals the number of bits set in the chunk_index_mask
	map->buckets_chunk_shift = __builtin_popcount(FWMAP_CHUNK_INDEX_MASK);

	map->extra_size = extra_size;
	// Index 0 is reserved (interpreted as a null pointer)
	map->extra_free_idx = 1;

	map->keys_in_chunk =
		index_size > keys_per_chunk ? keys_per_chunk : index_size;
	map->keys_chunk_cnt = keys_chunk_cnt;
	map->key_cursor = 0;

	map->values_in_chunk =
		index_size > values_per_chunk ? values_per_chunk : index_size;
	map->values_chunk_cnt = values_chunk_cnt;

	size_t extra_buckets_size = 0;
	fwmap_bucket_t **chunks = NULL;
	fwmap_bucket_t *extra_buckets = NULL;
	uint8_t **key_store = NULL;
	uint8_t **value_store = NULL;

	// Allocate index.
	uint32_t chunk_count =
		(map->index_mask >> map->buckets_chunk_shift) + 1;
	size_t chunks_array_size = sizeof(fwmap_bucket_t *) * chunk_count;
	if (!(chunks = memory_balloc(ctx, chunks_array_size))) {
		errno = ENOMEM;
		goto fail;
	}
	SET_OFFSET_OF(&map->buckets, chunks);

	size_t index_chunk_size =
		sizeof(fwmap_bucket_t) *
		((map->index_mask & FWMAP_CHUNK_INDEX_MASK) + 1);
	for (uint32_t i = 0; i < chunk_count; i++) {
		// Allocate with 64-byte alignment
		fwmap_bucket_t *chunk = fwmap_balloc_aligned(
			ctx, index_chunk_size, 64, &map->buckets_alloc_offset
		);
		if (!chunk) {
			// Stop point for the deallocation code.
			chunks[i] = NULL;
			errno = ENOMEM;
			goto fail;
		}
		// Verify 64-byte alignment
		assert(((uintptr_t)chunk & 63) == 0 &&
		       "Bucket chunk must be 64-byte aligned");
		memset(chunk, 0, index_chunk_size);
		SET_OFFSET_OF(&chunks[i], chunk);
	}

	// Extra buckets provide additional space for chaining without adding
	// keys and values. The map size remains limited to index_size.
	if (extra_size > 0) {
		extra_buckets_size = sizeof(fwmap_bucket_t) * extra_size;
		// Allocate with 64-byte alignment
		extra_buckets = fwmap_balloc_aligned(
			ctx,
			extra_buckets_size,
			64,
			&map->extra_buckets_alloc_offset
		);
		if (!extra_buckets) {
			errno = ENOMEM;
			goto fail;
		}
		// Verify 64-byte alignment
		assert(((uintptr_t)extra_buckets & 63) == 0 &&
		       "Extra buckets must be 64-byte aligned");
		memset(extra_buckets, 0, extra_buckets_size);
		SET_OFFSET_OF(&map->extra_buckets, extra_buckets);
	}

	// Key/Value store.
	// Allocate keys storage chunks array.
	size_t key_store_array_size = sizeof(uint8_t *) * map->keys_chunk_cnt;
	if (!(key_store = memory_balloc(ctx, key_store_array_size))) {
		errno = ENOMEM;
		goto fail;
	}
	SET_OFFSET_OF(&map->key_store, key_store);

	if (fwmap_allocate_chunks(
		    ctx,
		    (uint8_t **)key_store,
		    index_size,
		    map->keys_in_chunk,
		    map->keys_chunk_cnt,
		    map->key_size
	    ) == -1) {
		goto fail;
	}

	// Allocate values storage chunks array.
	size_t value_store_array_size =
		sizeof(uint8_t *) * map->values_chunk_cnt;
	if (!(value_store = memory_balloc(ctx, value_store_array_size))) {
		errno = ENOMEM;
		goto fail;
	}
	SET_OFFSET_OF(&map->value_store, value_store);

	if (fwmap_allocate_chunks(
		    ctx,
		    value_store,
		    index_size,
		    map->values_in_chunk,
		    map->values_chunk_cnt,
		    map->value_size
	    ) == -1) {
		goto fail;
	}

	return map;

fail:
	fwmap_destroy(map, ctx);
	return NULL;
}

static inline int64_t
fwmap_get_value_and_deadline(
	fwmap_t *map,
	uint64_t now,
	const void *key,
	void **value,
	rwlock_t **lock,
	uint64_t *deadline
) {
	fwmap_hash_fn_t hash_fn =
		(fwmap_hash_fn_t)fwmap_func_registry[map->hash_fn_id];
	fwmap_key_equal_fn_t key_equal_fn =
		(fwmap_key_equal_fn_t)fwmap_func_registry[map->key_equal_fn_id];

	uint64_t hash64 = hash_fn(key, map->key_size, map->hash_seed);
	uint32_t hash = (uint32_t)hash64;
	// uint32_t sec_hash = (uint32_t)(hash64 >> 32);
	// Use primary hash or secondary hash (fallback to avoid zero).
	// hash = hash ? hash : sec_hash;
	uint16_t sig = hash >> 16;
	sig = sig ? sig : 1;

	uint32_t chunk_idx =
		(hash & map->index_mask) >> map->buckets_chunk_shift;
	uint32_t bucket_idx = hash & map->index_mask & FWMAP_CHUNK_INDEX_MASK;

	fwmap_bucket_t *extra = ADDR_OF(&map->extra_buckets);
	fwmap_bucket_t **chunks = ADDR_OF(&map->buckets);
	fwmap_bucket_t *buckets = ADDR_OF(&chunks[chunk_idx]);
	fwmap_bucket_t *bucket = &buckets[bucket_idx];

	if (lock) {
		rwlock_read_lock(&bucket->lock);
		*lock = &bucket->lock;
	}

	while (bucket) {
		for (size_t i = 0; i < FWMAP_BUCKET_ENTRIES; i++) {
			if (bucket->sig[i] == sig &&
			    bucket->deadline[i] > now) {
				uint32_t key_idx = bucket->idx[i];
				uint8_t *other = fwmap_get_key(map, key_idx);
				if (key_equal_fn(key, other, map->key_size)) {
					if (value != NULL) {
						*value = fwmap_get_value(
							map, key_idx
						);
					}
					if (deadline != NULL) {
						*deadline = bucket->deadline[i];
					}
					return key_idx;
				}
			} else if (!bucket->sig[i]) {
				// Return early on first empty slot; no entries
				// should exist after an empty slot.
				return -1;
			}
		}

		// Index 0 represents a null pointer
		bucket = bucket->next ? &extra[bucket->next] : NULL;
	}
	return -1;
}

static inline int64_t
fwmap_get(
	fwmap_t *map,
	uint64_t now,
	const void *key,
	void **value,
	rwlock_t **lock
) {
	return fwmap_get_value_and_deadline(map, now, key, value, lock, NULL);
}

static inline fwmap_entry_t
fwmap_entry(
	fwmap_t *map,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const void *key,
	rwlock_t **lock
) {
	fwmap_hash_fn_t hash_fn =
		(fwmap_hash_fn_t)fwmap_func_registry[map->hash_fn_id];
	fwmap_key_equal_fn_t key_equal_fn =
		(fwmap_key_equal_fn_t)fwmap_func_registry[map->key_equal_fn_id];

	uint64_t hash64 = hash_fn(key, map->key_size, map->hash_seed);
	uint32_t hash = (uint32_t)hash64;
	// uint32_t sec_hash = (uint32_t)(hash64 >> 32);
	// Use primary hash or secondary hash (fallback to avoid zero).
	// hash = hash ? hash : sec_hash;
	uint16_t sig = hash >> 16;
	sig = sig ? sig : 1;

	uint32_t chunk_idx =
		(hash & map->index_mask) >> map->buckets_chunk_shift;
	uint32_t bucket_idx = hash & map->index_mask & FWMAP_CHUNK_INDEX_MASK;

	uint64_t deadline = now + ttl;

	fwmap_bucket_t *extra = ADDR_OF(&map->extra_buckets);
	fwmap_bucket_t **chunks = ADDR_OF(&map->buckets);
	fwmap_bucket_t *buckets = ADDR_OF(&chunks[chunk_idx]);
	fwmap_bucket_t *bucket = &buckets[bucket_idx];

	if (lock) {
		rwlock_write_lock(&bucket->lock);
		*lock = &bucket->lock;
	}

	size_t chain_length = 0;
	fwmap_bucket_t *last_bucket = bucket;

	bool has_free = false;
	uint32_t vacant_slot = 0;
	fwmap_bucket_t *bucket_to_insert = NULL;

	while (bucket) {
		chain_length += 1;

		// Search for and update existing key.
		for (uint32_t i = 0; i < FWMAP_BUCKET_ENTRIES; i++) {
			if (bucket->sig[i] == sig &&
			    bucket->deadline[i] > now) {
				uint32_t idx = bucket->idx[i];
				uint8_t *other = fwmap_get_key(map, idx);
				if (key_equal_fn(key, other, map->key_size)) {
					uint8_t *value_ptr =
						fwmap_get_value(map, idx);
					// Update deadline for existing entry.
					bucket->deadline[i] = deadline;
					fwmap_update_counters(
						map,
						worker_idx,
						chain_length,
						0,
						deadline
					);
					return (fwmap_entry_t
					){.idx = idx,
					  .key = other,
					  .value = value_ptr,
					  .empty = false};
				}
			} else if (!bucket_to_insert) {
				if (!bucket->sig[i]) {
					has_free = true;
					vacant_slot = i;
					bucket_to_insert = bucket;
					break;
				} else if (bucket->deadline[i] < now) {
					vacant_slot = i;
					bucket_to_insert = bucket;
				}
			}
		}
		last_bucket = bucket;

		if (has_free) {
			// If a free slot exists, there should be no next bucket
			// (free slots only occur at the end of a chain).
			break;
		}

		// Index 0 represents a null pointer
		bucket = bucket->next ? &extra[bucket->next] : NULL;
	}

	if (bucket_to_insert) {
		// Insert new key-value pair into an empty slot in existing
		// buckets.
		int64_t idx = (int64_t)bucket_to_insert->idx[vacant_slot];
		if (has_free) {
			idx = fwmap_next_free_key(map);
			if (idx == -1) {
				return (fwmap_entry_t){0};
			}
			bucket_to_insert->idx[vacant_slot] = (uint32_t)idx;
		}

		// Get the key slot and store the key.
		uint8_t *new_key = fwmap_get_key(map, (uint32_t)idx);
		uint8_t *value_ptr = fwmap_get_value(map, idx);

		// Store signature and key index in the bucket.
		bucket_to_insert->sig[vacant_slot] = sig;
		bucket_to_insert->deadline[vacant_slot] = deadline;

		// Update counters.
		fwmap_update_counters(
			map, worker_idx, chain_length, (int)has_free, deadline
		);

		return (fwmap_entry_t
		){.idx = idx, .key = new_key, .value = value_ptr, .empty = true
		};
	}

	// All slots in the existing chain are full; need to allocate a new
	// bucket.
	if (map->extra_free_idx >= map->extra_size) {
		// No more extra buckets available.
		return (fwmap_entry_t){0};
	}

	// Allocate new extra bucket.
	uint32_t new_bucket_idx =
		__atomic_fetch_add(&map->extra_free_idx, 1, __ATOMIC_RELAXED);
	if (new_bucket_idx >= map->extra_size) {
		return (fwmap_entry_t){0};
	}

	fwmap_bucket_t *new_bucket = &extra[new_bucket_idx];
	// Free extra buckets are already zero-initialized (zeroed at creation
	// and during clear calls).
	new_bucket->next = 0;

	// Allocate new key.
	int64_t idx = fwmap_next_free_key(map);
	if (idx == -1) {
		// No more space for keys.
		return (fwmap_entry_t){0};
	}

	// Store key and value.
	uint8_t *new_key = fwmap_get_key(map, (uint32_t)idx);
	uint8_t *value_ptr = fwmap_get_value(map, idx);

	// Initialize new bucket with the key.
	new_bucket->sig[0] = sig;
	new_bucket->idx[0] = (uint32_t)idx;
	new_bucket->deadline[0] = deadline;

	last_bucket->next = new_bucket_idx;

	// Update counters.
	chain_length++;
	fwmap_update_counters(
		map, worker_idx, chain_length, 1, new_bucket->deadline[0]
	);

	return (fwmap_entry_t
	){.idx = idx, .key = new_key, .value = value_ptr, .empty = true};
}

static inline int64_t
fwmap_put(
	fwmap_t *map,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const void *key,
	const void *value,
	rwlock_t **lock
) {
	fwmap_copy_key_fn_t copy_key_fn =
		(fwmap_copy_key_fn_t)fwmap_func_registry[map->copy_key_fn_id];
	fwmap_copy_value_fn_t copy_value_fn = (fwmap_copy_value_fn_t
	)fwmap_func_registry[map->copy_value_fn_id];

	fwmap_entry_t entry = fwmap_entry(map, worker_idx, now, ttl, key, lock);
	if (!entry.key) {
		return -1;
	}
	if (entry.empty) {
		copy_key_fn(entry.key, key, map->key_size);
	}
	copy_value_fn(entry.value, value, map->value_size);

	return (int64_t)entry.idx;
}

static inline void
fwmap_clear(fwmap_t *map) {
	if (!map) {
		return;
	}

	// 1. Clear all primary buckets.
	fwmap_bucket_t **chunks = ADDR_OF(&map->buckets);
	if (chunks) {
		uint32_t chunk_count =
			(map->index_mask >> map->buckets_chunk_shift) + 1;
		size_t index_chunk_size =
			sizeof(fwmap_bucket_t) *
			((map->index_mask & FWMAP_CHUNK_INDEX_MASK) + 1);

		for (uint32_t i = 0; i < chunk_count; i++) {
			fwmap_bucket_t *buckets = ADDR_OF(&chunks[i]);
			if (buckets) {
				memset(buckets, 0, index_chunk_size);
			}
		}
	}

	// 2. Clear extra buckets.
	if (map->extra_buckets) {
		fwmap_bucket_t *extra_buckets = ADDR_OF(&map->extra_buckets);
		memset(extra_buckets,
		       0,
		       sizeof(fwmap_bucket_t) * map->extra_size);
	}

	// 3. Reset extra bucket free index.
	map->extra_free_idx = 1;

	// 4. Reset key cursor.
	map->key_cursor = 0;

	// 5. Reset counters.
	memset(map->counters, 0, sizeof(fwmap_counter_t) * map->worker_count);
}

/**
 * Thread-safe wrapper for fwmap_put using a global read-write lock.
 * Simple wrapper that acquires a write lock, calls the unsafe put, then
 * releases the lock.
 */
static inline int
fwmap_put_safe(
	fwmap_t *map,
	uint16_t worker_idx,
	uint64_t now,
	uint64_t ttl,
	const void *key,
	const void *value
) {
	rwlock_t *lock = NULL;
	int result = fwmap_put(map, worker_idx, now, ttl, key, value, &lock);
	if (lock) {
		rwlock_write_unlock(lock);
	}
	return result;
}

// Global function registry - statically initialized.
static void *fwmap_func_registry[FWMAP_FUNC_COUNT] = {
	[FWMAP_UNINITIALIZED] = NULL,
	[FWMAP_HASH_FNV1A] = (void *)fwmap_hash_fnv1a,
	[FWMAP_KEY_EQUAL_DEFAULT] = (void *)fwmap_default_key_equal,
	[FWMAP_RAND_DEFAULT] = (void *)fwmap_rand_default,
	[FWMAP_RAND_SECURE] = (void *)fwmap_rand_secure,
	[FWMAP_COPY_KEY_DEFAULT] = (void *)fwmap_default_copy_key,
	[FWMAP_COPY_VALUE_DEFAULT] = (void *)fwmap_default_copy_value,
	[FWMAP_MERGE_VALUE_DEFAULT] = (void *)fwmap_default_merge_value,
	[FWMAP_COPY_KEY_FW4] = (void *)fwmap_copy_key_fw4,
	[FWMAP_COPY_KEY_FW6] = (void *)fwmap_copy_key_fw6,
	[FWMAP_COPY_VALUE_FWSTATE] = (void *)fwmap_copy_value_fwstate,
	[FWMAP_MERGE_VALUE_FWSTATE] = (void *)fwmap_merge_value_fwstate,
	[FWMAP_KEY_EQUAL_FW4] = (void *)fwmap_fw4_key_equal,
	[FWMAP_KEY_EQUAL_FW6] = (void *)fwmap_fw6_key_equal
};
