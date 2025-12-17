/*
 * FWMap Basic Functionality Tests
 *
 * Tests core operations of the TTL-based hash map implementation including
 * insertion, retrieval, updates, and collision handling under various
 * conditions.
 */

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

#define ARENA_SIZE_MB 512
#define ARENA_SIZE ((1 << 20) * ARENA_SIZE_MB)
#define DEFAULT_TTL 50000
#define WORKER_ID 0

/* Global time counter for TTL expiration testing */
volatile uint64_t now = 0;

/*
 * Initialize a standard configuration for testing.
 * Uses integer keys/values with default hash and comparison functions.
 */
static void
setup_test_config(
	fwmap_config_t *cfg, uint32_t idx_size, uint32_t extra_buckets
) {
	memset(cfg, 0, sizeof(*cfg));
	cfg->key_size = sizeof(int);
	cfg->value_size = sizeof(int);
	cfg->hash_seed = 0;
	cfg->worker_count = 1;
	cfg->hash_fn_id = FWMAP_HASH_FNV1A;
	cfg->key_equal_fn_id = FWMAP_KEY_EQUAL_DEFAULT;
	cfg->rand_fn_id = FWMAP_RAND_DEFAULT;
	cfg->index_size = idx_size;
	cfg->extra_bucket_count = extra_buckets;
}

/*
 * Verify compile-time constants and invariants.
 */
static void
verify_constants(void) {
	printf("\n--- Constants Verification ---\n");
	/* Bucket size must match struct size exactly */
	assert(FWMAP_BUCKET_SIZE == sizeof(fwmap_bucket_t));

	/* Chunk index max size must be power of 2 for efficient masking */
	assert(align_up_pow2(FWMAP_CHUNK_INDEX_MAX_SIZE) ==
	       FWMAP_CHUNK_INDEX_MAX_SIZE);

	/* Chunk mask must be one less than a power of 2 */
	assert(align_up_pow2(FWMAP_CHUNK_INDEX_MASK + 1) ==
	       FWMAP_CHUNK_INDEX_MASK + 1);

	printf("  Constants verification passed\n");
}

/*
 * Test basic map lifecycle: creation, insertion, retrieval, update, and
 * cleanup.
 */
static void
test_lifecycle(void *arena) {
	printf("\n--- Lifecycle Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "lifecycle");

	fwmap_config_t cfg;
	setup_test_config(&cfg, 128, 8);
	cfg.hash_seed = 0x12345678;

	/* Create map */
	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);
	assert(fwmap_empty(map));
	assert(fwmap_size(map) == 0);

	/* Insert first entry */
	int k1 = 777, v1 = 100;
	int ret = fwmap_put(map, WORKER_ID, now, DEFAULT_TTL, &k1, &v1, NULL);
	assert(ret >= 0);
	assert(fwmap_size(map) == 1);
	assert(!fwmap_empty(map));

	/* Retrieve and verify */
	int *retrieved = NULL;
	ret = fwmap_get(map, now, &k1, (void **)&retrieved, NULL);
	assert(ret >= 0);
	assert(*retrieved == 100);

	/* Update existing entry */
	int v2 = 200;
	ret = fwmap_put(map, WORKER_ID, now, DEFAULT_TTL, &k1, &v2, NULL);
	assert(ret >= 0);
	assert(fwmap_size(map) == 1); /* Size remains 1 on update */

	ret = fwmap_get(map, now, &k1, (void **)&retrieved, NULL);
	assert(ret >= 0);
	assert(*retrieved == 200);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "lifecycle");
	printf("  Lifecycle test passed\n");
}

/*
 * Test bulk operations with sequential keys.
 * Verifies map can handle moderate load and maintains consistency.
 */
static void
test_bulk_operations(void *arena) {
	printf("\n--- Bulk Operations Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "bulk_ops");

	fwmap_config_t cfg;
	setup_test_config(&cfg, 256, 16);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	/* Insert 100 sequential entries */
	const size_t num_entries = 100;
	for (size_t i = 0; i < num_entries; i++) {
		int key = i;
		int value = i * 10;
		int ret = fwmap_put(
			map, WORKER_ID, now, DEFAULT_TTL, &key, &value, NULL
		);
		assert(ret >= 0);
	}

	assert(fwmap_size(map) == num_entries);

	/* Verify all entries are retrievable with correct values */
	for (size_t i = 0; i < num_entries; i++) {
		int key = i;
		int *value = NULL;
		int ret = fwmap_get(map, now, &key, (void **)&value, NULL);
		assert(ret >= 0);
		assert(*value == (int)(i * 10));
	}

	/* Update subset of entries */
	for (size_t i = 0; i < num_entries; i += 10) {
		int key = i;
		int new_value = i * 100;
		int ret = fwmap_put(
			map, WORKER_ID, now, DEFAULT_TTL, &key, &new_value, NULL
		);
		assert(ret >= 0);
	}

	/* Verify updates took effect */
	for (size_t i = 0; i < num_entries; i++) {
		int key = i;
		int *value = NULL;
		int ret = fwmap_get(map, now, &key, (void **)&value, NULL);
		assert(ret >= 0);

		int expected = (i % 10 == 0) ? i * 100 : i * 10;
		assert(*value == expected);
	}

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "bulk_ops");
	printf("  Bulk operations test passed\n");
}

/*
 * Custom hash function that forces all keys to collide.
 * Used to stress-test collision handling and chaining logic.
 */
static uint64_t
collision_hash(const void *key, size_t key_size, uint32_t seed) {
	(void)key;
	(void)key_size;
	(void)seed;
	return 0x12345678ULL;
}

/*
 * Test collision handling by forcing all entries into same bucket chain.
 * Verifies that chaining works correctly and all entries remain accessible.
 */
static void
test_collision_chains(void *arena) {
	printf("\n--- Collision Handling Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "collisions");

	fwmap_config_t cfg;
	setup_test_config(&cfg, 1000, 1000);

	/* Temporarily replace hash function to force collisions */
	void *original_hash = fwmap_func_registry[FWMAP_HASH_FNV1A];
	fwmap_func_registry[FWMAP_HASH_FNV1A] = (void *)collision_hash;

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	/* Insert many entries that will all collide */
	const size_t collision_count = 1000;
	for (size_t i = 0; i < collision_count; i++) {
		int key = i;
		int value = i * 2;
		int ret = fwmap_put(
			map, WORKER_ID, now, DEFAULT_TTL, &key, &value, NULL
		);
		assert(ret >= 0);
	}

	assert(fwmap_size(map) == collision_count);

	/* Verify all colliding entries are still retrievable */
	for (size_t i = 0; i < collision_count; i++) {
		int key = i;
		int *value = NULL;
		int ret = fwmap_get(map, now, &key, (void **)&value, NULL);
		assert(ret >= 0);
		assert(*value == (int)(i * 2));
	}

	/* Check chain statistics */
	size_t max_chain = fwmap_max_chain_length(map);
	printf("    Max chain length: %zu entries\n", max_chain);
	assert(max_chain > 0); /* Must have chaining due to forced collisions */

	fwmap_stats_t stats = fwmap_get_stats(map);
	printf("    Memory usage: %zu bytes\n", stats.memory_used);

	fwmap_destroy(map, ctx);

	/* Restore original hash function */
	fwmap_func_registry[FWMAP_HASH_FNV1A] = original_hash;

	verify_memory_leaks(ctx, "collisions");
	printf("  Collision handling test passed\n");
}

/*
 * Test TTL expiration behavior.
 * Verifies that expired entries are not returned by get operations.
 */
static void
test_ttl_expiration(void *arena) {
	printf("\n--- TTL Expiration Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "ttl_expiry");

	fwmap_config_t cfg;
	setup_test_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	/* Insert entry with short TTL */
	int key = 42;
	int value = 999;
	uint64_t short_ttl = 100;
	int ret = fwmap_put(map, WORKER_ID, now, short_ttl, &key, &value, NULL);
	assert(ret >= 0);

	/* Entry should be retrievable before expiration */
	int *retrieved = NULL;
	ret = fwmap_get(map, now, &key, (void **)&retrieved, NULL);
	assert(ret >= 0);
	assert(*retrieved == 999);

	/* Advance time past TTL */
	now += short_ttl + 1;

	/* Entry should no longer be retrievable */
	ret = fwmap_get(map, now, &key, (void **)&retrieved, NULL);
	assert(ret < 0); /* Must fail to find expired entry */

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "ttl_expiry");
	printf("  TTL expiration test passed\n");
}

/*
 * Test direct entry access for zero-copy operations.
 */
static void
test_entry_access(void *arena) {
	printf("\n--- Entry Access Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "entry_access");

	fwmap_config_t cfg;
	setup_test_config(&cfg, 128, 8);

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	/* Get entry for new key - should allocate slot */
	int key = 42;
	fwmap_entry_t entry =
		fwmap_entry(map, WORKER_ID, now, DEFAULT_TTL, &key, NULL);
	assert(entry.key != NULL);
	assert(entry.value != NULL);
	assert(entry.empty); /* Entry is newly allocated */

	/* Write directly to entry */
	*(int *)entry.key = key;
	*(int *)entry.value = 1000;

	/* Verify via get */
	int *retrieved = NULL;
	int ret = fwmap_get(map, now, &key, (void **)&retrieved, NULL);
	assert(ret >= 0);
	assert(*retrieved == 1000);

	/* Get entry for existing key - should return same slot */
	entry = fwmap_entry(map, WORKER_ID, now, DEFAULT_TTL, &key, NULL);
	assert(!entry.empty); /* Entry already exists */
	assert(*(int *)entry.value == 1000);

	/* Update in place */
	*(int *)entry.value = 2000;

	ret = fwmap_get(map, now, &key, (void **)&retrieved, NULL);
	assert(ret >= 0);
	assert(*retrieved == 2000);

	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "entry_access");
	printf("  Entry access test passed\n");
}

/*
 * Test map capacity limits across multiple chunks.
 * Uses large keys/values to force chunking, then fills to near capacity.
 */
static void
test_capacity_limits(void *arena) {
	printf("\n--- Capacity Limits Test ---\n");
	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "capacity");

	/*
	 * Strategy: Use 2KB keys and 4KB values to force chunking.
	 * MEMORY_BLOCK_ALLOCATOR_MAX_SIZE = 64MB
	 * - Keys: 64MB / 2KB = 32K per chunk
	 * - Values: 64MB / 4KB = 16K per chunk
	 * Use 40K capacity to span 2 key chunks and 3 value chunks.
	 * Total: 40K × 6KB = 240MB, fits in 400MB arena.
	 */
	fwmap_config_t cfg;
	memset(&cfg, 0, sizeof(cfg));
	cfg.key_size = 2048;   /* 2KB keys */
	cfg.value_size = 4096; /* 4KB values */
	cfg.hash_seed = 0x42;
	cfg.worker_count = 1;
	cfg.hash_fn_id = FWMAP_HASH_FNV1A;
	cfg.key_equal_fn_id = FWMAP_KEY_EQUAL_DEFAULT;
	cfg.rand_fn_id = FWMAP_RAND_DEFAULT;
	cfg.index_size = 40000;
	cfg.extra_bucket_count =
		4000; /* Extra buckets for collision handling */

	fwmap_t *map = fwmap_new(&cfg, ctx);
	assert(map != NULL);

	printf("    Chunks: %u keys, %u values (extra buckets: %u)\n",
	       map->keys_chunk_cnt,
	       map->values_chunk_cnt,
	       map->extra_size);
	assert(map->keys_chunk_cnt >= 2);
	assert(map->values_chunk_cnt >= 2);

	/* Allocate buffers once */
	char *key_buf = malloc(cfg.key_size);
	char *val_buf = malloc(cfg.value_size);
	assert(key_buf && val_buf);

	/* Fill to 90% capacity */
	const size_t target = (size_t)(cfg.index_size * 0.9);
	size_t inserted = 0;
	size_t failed = 0;

	for (size_t i = 0; i < cfg.index_size; i++) {
		memset(key_buf, 0, cfg.key_size);
		memset(val_buf, 0, cfg.value_size);
		*(uint64_t *)key_buf = i;
		*(uint64_t *)val_buf = i * 7;

		int ret = fwmap_put(
			map, WORKER_ID, now, DEFAULT_TTL, key_buf, val_buf, NULL
		);
		if (ret >= 0) {
			inserted++;
			if (inserted >= target)
				break;
		} else {
			failed++;
		}
	}

	size_t fill_pct = (inserted * 100) / cfg.index_size;
	printf("    Filled: %zu/%u entries (%zu%%, %zu failed)\n",
	       inserted,
	       cfg.index_size,
	       fill_pct,
	       failed);
	assert(fill_pct >= 85); /* Must achieve at least 85% fill rate */

	/* Verify sample entries from different chunks */
	size_t verified = 0;
	for (size_t i = 0; i < inserted; i += 2000) {
		memset(key_buf, 0, cfg.key_size);
		*(uint64_t *)key_buf = i;

		void *value = NULL;
		int ret = fwmap_get(map, now, key_buf, &value, NULL);
		if (ret >= 0) {
			assert(*(uint64_t *)value == i * 7);
			verified++;
		}
	}
	printf("    Verified %zu sample entries\n", verified);

	/* Test update at high capacity */
	memset(key_buf, 0, cfg.key_size);
	memset(val_buf, 0, cfg.value_size);
	*(uint64_t *)key_buf = 1000;
	*(uint64_t *)val_buf = 999999;

	int ret = fwmap_put(
		map, WORKER_ID, now, DEFAULT_TTL, key_buf, val_buf, NULL
	);
	assert(ret >= 0);

	void *updated = NULL;
	ret = fwmap_get(map, now, key_buf, &updated, NULL);
	assert(ret >= 0);
	assert(*(uint64_t *)updated == 999999);

	fwmap_stats_t stats = fwmap_get_stats(map);
	printf("    Stats: %u entries, %u max chain, %zu bytes\n",
	       stats.total_elements,
	       stats.max_chain_length,
	       stats.memory_used);

	free(key_buf);
	free(val_buf);
	fwmap_destroy(map, ctx);
	verify_memory_leaks(ctx, "capacity");
	printf("  Capacity limits test passed\n");
}

int
main(void) {
	printf("%s%s=== FWMap Basic Tests ===%s\n\n", C_BOLD, C_WHITE, C_RESET);

	void *arena = allocate_locked_memory(ARENA_SIZE);
	if (!arena) {
		fprintf(stderr,
			"Failed to allocate %dMB test arena\n",
			ARENA_SIZE_MB);
		return EXIT_FAILURE;
	}

	printf("%s%sRunning test suite...%s\n", C_BOLD, C_BLUE, C_RESET);

	verify_constants();
	test_lifecycle(arena);
	test_bulk_operations(arena);
	test_collision_chains(arena);
	test_ttl_expiration(arena);
	test_entry_access(arena);
	test_capacity_limits(arena);

	free_arena(arena, ARENA_SIZE);

	printf("\n%s%s=== All tests passed ===%s\n", C_BOLD, C_GREEN, C_RESET);
	return EXIT_SUCCESS;
}
