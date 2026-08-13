// Regression tests for the fwtable layer chain: lock discipline across
// the head-miss paths of fwtable_lookup_internal.
//
// A lookup that misses the head layer and probes deeper layers must
// release the head bucket's read lock before the deeper probes store
// their own lock through the same out-parameter. Without that release
// the head lock leaks, and the same thread's later fwtable_insert on
// the same key write-locks that bucket and spins forever on its own
// read lock — the exact sequence a post-rotation sync packet drives.

#include "lib/fwstate/fwtable.h"
#include "test_utils.h"
#include <assert.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ARENA_SIZE_MB 16
#define ARENA_SIZE (1 << 20) * ARENA_SIZE_MB

static uint64_t now_time = 1000000;

static fwmap_config_t
table_test_config(void) {
	return (fwmap_config_t){
		.key_size = sizeof(int),
		.value_size = sizeof(int),
		.hash_seed = 0xdeadbeef,
		.worker_count = 1,
		.index_size = 128,
		.extra_bucket_count = 16,
	};
}

// Insert a key, then rotate a fresh layer on top, then run the
// suppression-path sequence on the stale key: lookup (head miss, deeper
// hit), unlock the returned lock, insert the same key again. The insert
// targets the head bucket the lookup probed first, so a leaked head
// read lock deadlocks the single-threaded test run.
static void
test_lookup_head_miss_then_insert(void *arena) {
	fprintf(stderr, "Testing lookup head-miss lock release...\n");

	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "fwtable_locks");

	fwmap_config_t config = table_test_config();

	fwtable_t table = {0};
	assert(fwtable_insert_layer_cp(&table, &config, ctx) == 0);

	int key = 42, value = 4242;
	assert(fwtable_insert(&table, 0, now_time, 60, &key, &value, NULL) >= 0
	);

	// Rotation: a fresh head layer, the entry now lives one layer down.
	assert(fwtable_insert_layer_cp(&table, &config, ctx) == 0);

	void *found = NULL;
	rwlock_t *lock = NULL;
	uint64_t deadline = 0;
	bool from_stale = false;
	int64_t ret = fwtable_lookup_with_deadline(
		&table, now_time, &key, &found, &lock, &deadline, &from_stale
	);
	assert(ret >= 0);
	assert(from_stale);
	if (lock) {
		rwlock_read_unlock(lock);
	}

	// The same key re-inserted, the way the sync suppression path does:
	// a non-NULL lock out-parameter makes the insert write-lock the head
	// bucket — the very bucket the lookup's first probe read-locked.
	// Completing (not hanging) proves that lock was released.
	int new_value = 9;
	rwlock_t *insert_lock = NULL;
	int64_t insert_ret = fwtable_insert(
		&table, 0, now_time, 60, &key, &new_value, &insert_lock
	);
	assert(insert_ret >= 0);
	if (insert_lock) {
		rwlock_write_unlock(insert_lock);
	}

	// A full miss must release every probed lock the same way: insert
	// a key hashing into an arbitrary bucket right after a miss.
	int missing = 77;
	lock = NULL;
	found = NULL;
	ret = fwtable_lookup_with_deadline(
		&table,
		now_time,
		&missing,
		&found,
		&lock,
		&deadline,
		&from_stale
	);
	assert(ret < 0);
	if (lock) {
		rwlock_read_unlock(lock);
	}
	int fresh = 7;
	insert_lock = NULL;
	insert_ret = fwtable_insert(
		&table, 0, now_time, 60, &missing, &fresh, &insert_lock
	);
	assert(insert_ret >= 0);
	if (insert_lock) {
		rwlock_write_unlock(insert_lock);
	}

	// Teardown: free both layers.
	fwmap_t *layer = ADDR_OF(&table.head);
	while (layer != NULL) {
		fwmap_t *next = (fwmap_t *)ADDR_OF(&layer->next);
		fwmap_free(layer, ctx);
		layer = next;
	}

	verify_memory_leaks(ctx, "fwtable_locks");
	fprintf(stderr, "OK\n");
}

int
main(void) {
	printf("%s%s=== FWTable Lock Tests ===%s\n\n", C_BOLD, C_WHITE, C_RESET
	);

	void *arena = allocate_locked_memory(ARENA_SIZE);
	if (!arena) {
		fprintf(stderr,
			"Failed to allocate %dMB test arena\n",
			ARENA_SIZE_MB);
		return EXIT_FAILURE;
	}

	test_lookup_head_miss_then_insert(arena);

	free_arena(arena, ARENA_SIZE);

	printf("\n%s%s=== All tests passed ===%s\n", C_BOLD, C_GREEN, C_RESET);
	return EXIT_SUCCESS;
}
