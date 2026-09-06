// Regression tests for the fwtable layer chain: lock discipline across
// the head-miss paths of fwtable_lookup_internal, and the predicate a
// caller uses to decide whether a reclamation round has anything to do.
//
// A lookup that misses the head layer and probes deeper layers must
// release the head bucket's read lock before the deeper probes store
// their own lock through the same out-parameter. Without that release
// the head lock leaks, and the same thread's later fwtable_insert on
// the same key write-locks that bucket and spins forever on its own
// read lock — the exact sequence a post-rotation sync packet drives.

#include "lib/statemap/fwtable.h"
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

// A chain that has never rotated offers nothing to reclaim, and neither
// does one whose oldest layer is still live. Only once that layer has
// drained, or once a previous round parked something, does the
// predicate report work — the caller pays generation barriers on its
// word, so a false positive costs a wasted round and a false negative
// strands a layer.
static void
test_reclaimable_predicate(void *arena) {
	fprintf(stderr, "Testing reclaimable predicate...\n");

	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "fwtable_reclaim");

	fwmap_config_t config = table_test_config();
	fwtable_t table = {0};

	assert(!fwtable_has_reclaimable(&table, now_time));

	assert(fwtable_insert_layer_cp(&table, &config, ctx) == 0);
	assert(!fwtable_has_reclaimable(&table, now_time));
	assert(fwtable_stale_count(&table) == 0);

	// Rotation leaves the first layer behind the head carrying no
	// entries, so its deadline is already behind us.
	assert(fwtable_insert_layer_cp(&table, &config, ctx) == 0);
	assert(fwtable_has_reclaimable(&table, now_time));

	assert(fwtable_unlink_stale_cp(&table, now_time) == 0);
	assert(fwtable_stale_count(&table) == 1);
	assert(fwtable_has_reclaimable(&table, now_time));

	fwtable_free_stale(&table, ctx);
	assert(fwtable_stale_count(&table) == 0);
	assert(!fwtable_has_reclaimable(&table, now_time));

	fprintf(stderr, "OK\n");
}

// A tail layer that still holds a live entry is not reclaimable.
//
// Rotation alone does not release the layer behind the head: it keeps
// answering lookups until every deadline in it has passed, so the
// predicate has to weigh the entries and not just the shape of the chain.
static void
test_reclaimable_predicate_live_tail(void *arena) {
	fprintf(stderr, "Testing reclaimable predicate with a live tail...\n");

	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "fwtable_live_tail");

	fwmap_config_t config = table_test_config();
	fwtable_t table = {0};

	assert(fwtable_insert_layer_cp(&table, &config, ctx) == 0);

	int key = 7, value = 77;
	assert(fwtable_insert(&table, 0, now_time, 60, &key, &value, NULL) >= 0
	);

	// Rotation moves the live entry one layer down, where reclamation
	// looks for drained layers.
	assert(fwtable_insert_layer_cp(&table, &config, ctx) == 0);

	assert(!fwtable_has_reclaimable(&table, now_time));
	assert(!fwtable_has_reclaimable(&table, now_time + 59));

	// The decision and the predicate must agree: neither releases a
	// layer whose last entry outlives the time asked about.
	assert(fwtable_unlink_stale_cp(&table, now_time + 59) == 0);
	assert(fwtable_stale_count(&table) == 0);

	assert(fwtable_has_reclaimable(&table, now_time + 60));
	assert(fwtable_unlink_stale_cp(&table, now_time + 60) == 0);
	assert(fwtable_stale_count(&table) == 1);

	fwtable_free_stale(&table, ctx);

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
	test_reclaimable_predicate(arena);
	test_reclaimable_predicate_live_tail(arena);

	free_arena(arena, ARENA_SIZE);

	printf("\n%s%s=== All tests passed ===%s\n", C_BOLD, C_GREEN, C_RESET);
	return EXIT_SUCCESS;
}
