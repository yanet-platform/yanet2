#include "lib/fwstate/layermap.h"
#include "test_utils.h"
#include <assert.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <unistd.h>

#define ARENA_SIZE (1 << 20) * 64 // MB arena
volatile uint64_t now_time = 0;

void
test_layermap_basic_operations(void *arena) {
	fprintf(stderr, "Testing layermap basic operations...\n");
	uint16_t worker_idx = 0;

	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "layermap_basic");

	fwmap_config_t config = {
		.key_size = sizeof(int),
		.value_size = sizeof(int),
		.hash_seed = 0xdeadbeef,
		.worker_count = 1,
		.index_size = 128,
		.extra_bucket_count = 16,
	};

	fwmap_t *active_layer = fwmap_new(&config, ctx);
	assert(active_layer != NULL);

	// Test insertion
	int key1 = 123, value1 = 456;
	int64_t ret = layermap_put(
		active_layer,
		worker_idx,
		now_time,
		now_time + 60,
		&key1,
		&value1,
		NULL
	);
	assert(ret >= 0);

	// Test retrieval
	int *found_value = NULL;
	bool value_from_stale = false;
	ret = layermap_get(
		active_layer,
		now_time,
		&key1,
		(void **)&found_value,
		NULL,
		&value_from_stale
	);
	assert(ret >= 0);
	assert(*found_value == value1);
	assert(!value_from_stale);

	// Test update
	int value2 = 789;
	ret = layermap_put(
		active_layer,
		worker_idx,
		now_time,
		now_time + 60,
		&key1,
		&value2,
		NULL
	);
	assert(ret >= 0);

	ret = layermap_get(
		active_layer,
		now_time,
		&key1,
		(void **)&found_value,
		NULL,
		&value_from_stale
	);
	assert(ret >= 0);
	assert(*found_value == value2);

	// Test rotation by creating a new layer and linking it
	SET_OFFSET_OF(&active_layer, active_layer);
	ret = layermap_insert_new_layer_cp(&active_layer, &config, ctx);
	assert(ret == 0);
	// Reload active_layer using ADDR_OF to dereference the offset
	active_layer = ADDR_OF(&active_layer);

	// After rotation, the old active layer becomes read-only.
	// The key should still be retrievable.
	ret = layermap_get(
		active_layer,
		now_time,
		&key1,
		(void **)&found_value,
		NULL,
		&value_from_stale
	);
	assert(ret >= 0);
	assert(*found_value == value2);
	assert(value_from_stale); // Should come from stale layer

	// Insert a new key into the new active layer
	int key2 = 999, value3 = 111;
	ret = layermap_put(
		active_layer,
		worker_idx,
		now_time,
		now_time + 60,
		&key2,
		&value3,
		NULL
	);
	assert(ret >= 0);

	// Both keys should be retrievable
	ret = layermap_get(
		active_layer,
		now_time,
		&key1,
		(void **)&found_value,
		NULL,
		&value_from_stale
	);
	assert(ret >= 0);
	assert(*found_value == value2);

	ret = layermap_get(
		active_layer,
		now_time,
		&key2,
		(void **)&found_value,
		NULL,
		&value_from_stale
	);
	assert(ret >= 0);
	assert(*found_value == value3);

	// Both keys should be outdated
	ret = layermap_get(
		active_layer,
		now_time + 61,
		&key1,
		(void **)&found_value,
		NULL,
		&value_from_stale
	);
	assert(ret < 0);

	ret = layermap_get(
		active_layer,
		now_time + 61,
		&key2,
		(void **)&found_value,
		NULL,
		&value_from_stale
	);
	assert(ret < 0);

	// Cleanup: destroy all layers in the chain
	fwmap_t *layer = active_layer;
	while (layer) {
		fwmap_t *next = (fwmap_t *)ADDR_OF(&layer->next);
		fwmap_free(layer, ctx);
		layer = next;
	}

	verify_memory_leaks(ctx, "layermap_basic_operations");
	memory_context_fini(ctx);
	fprintf(stderr, "Layermap basic operations test PASSED\n");
}

#define ALLOC_FAILURE_COARSE_CHUNK_SIZE (64 * 1024)
#define ALLOC_FAILURE_MAX_COARSE_BLOCKS 4096
#define ALLOC_FAILURE_MAX_FINE_BLOCKS 65536

// Fills ctx to capacity so that a subsequent allocation of
// sizeof(layermap_list_t) bytes deterministically fails. A coarse pass of
// large chunks drains most of the arena quickly, keeping the bookkeeping
// array small, then a fine pass of layermap_list_t sized blocks exhausts
// whatever remains.
static void
exhaust_memory_context(
	struct memory_context *ctx,
	void **coarse_blocks,
	size_t *coarse_count,
	void **fine_blocks,
	size_t *fine_count
) {
	*coarse_count = 0;
	while (*coarse_count < ALLOC_FAILURE_MAX_COARSE_BLOCKS) {
		void *block =
			memory_balloc(ctx, ALLOC_FAILURE_COARSE_CHUNK_SIZE);
		if (block == NULL) {
			break;
		}
		coarse_blocks[(*coarse_count)++] = block;
	}
	assert(*coarse_count < ALLOC_FAILURE_MAX_COARSE_BLOCKS);

	*fine_count = 0;
	while (*fine_count < ALLOC_FAILURE_MAX_FINE_BLOCKS) {
		void *block = memory_balloc(ctx, sizeof(layermap_list_t));
		if (block == NULL) {
			break;
		}
		fine_blocks[(*fine_count)++] = block;
	}
	assert(*fine_count < ALLOC_FAILURE_MAX_FINE_BLOCKS);
}

static size_t
count_chain_layers(fwmap_t *layer) {
	size_t count = 0;
	while (layer) {
		count++;
		layer = (fwmap_t *)ADDR_OF(&layer->next);
	}
	return count;
}

// Verifies that a bookkeeping-node allocation failure during trim leaves the
// layer chain and the outdated list fully consistent, with no orphaned
// layer, and that a retried trim recovers once memory is available again.
void
test_layermap_alloc_failure_rollback(void *arena) {
	fprintf(stderr,
		"Testing layermap trim allocation-failure rollback...\n");

	struct memory_context *ctx = init_context_from_arena(
		arena, ARENA_SIZE, "layermap_alloc_failure"
	);

	fwmap_config_t config = {
		.key_size = sizeof(int),
		.value_size = sizeof(int),
		.hash_seed = 0xdeadbeef,
		.worker_count = 1,
		.index_size = 128,
		.extra_bucket_count = 16,
	};

	uint64_t now = 0;

	// Allocate the active-layer pointer in the arena (not on the stack)
	// because layermap functions and ADDR_OF require it to reside in
	// shared memory.
	fwmap_t **active_layer_offset = memory_balloc(ctx, sizeof(fwmap_t *));
	assert(active_layer_offset != NULL);

	fwmap_t *layer1 = fwmap_new(&config, ctx);
	assert(layer1 != NULL);
	SET_OFFSET_OF(active_layer_offset, layer1);

	int key1 = 1, value1 = 100;
	int64_t ret =
		layermap_put(layer1, 0, now, now + 60, &key1, &value1, NULL);
	assert(ret >= 0);

	int insert_result =
		layermap_insert_new_layer_cp(active_layer_offset, &config, ctx);
	assert(insert_result == 0);
	fwmap_t *layer2 = ADDR_OF(active_layer_offset);

	int key2 = 2, value2 = 200;
	ret = layermap_put(layer2, 0, now, now + 60, &key2, &value2, NULL);
	assert(ret >= 0);

	insert_result =
		layermap_insert_new_layer_cp(active_layer_offset, &config, ctx);
	assert(insert_result == 0);
	fwmap_t *layer3 = ADDR_OF(active_layer_offset);

	// layer3 is active and never examined by trim. layer2 and layer1
	// both carry a max deadline of now + 60, so trimming at now + 61
	// makes both of them stale.
	uint64_t trim_now = now + 61;
	assert(count_chain_layers(layer3) == 3);

	static void *coarse_blocks[ALLOC_FAILURE_MAX_COARSE_BLOCKS];
	static void *fine_blocks[ALLOC_FAILURE_MAX_FINE_BLOCKS];
	size_t coarse_count = 0, fine_count = 0;
	exhaust_memory_context(
		ctx, coarse_blocks, &coarse_count, fine_blocks, &fine_count
	);
	void *probe = memory_balloc(ctx, sizeof(layermap_list_t));
	assert(probe == NULL);

	// Total failure: no layermap_list_t node can be allocated at all, so
	// the very first stale layer must fail to unlink. The chain must be
	// left fully intact.
	layermap_list_t *outdated_head = NULL;
	int trim_ret = layermap_trim_stale_layers_cp(
		active_layer_offset, ctx, trim_now, &outdated_head
	);
	assert(trim_ret == -1);
	assert(outdated_head == NULL);
	assert(count_chain_layers(ADDR_OF(active_layer_offset)) == 3);

	// Partial failure: free exactly one layermap_list_t sized block, so
	// the first stale layer (layer2) can be collected but the second
	// (layer1) still fails and must stay linked.
	assert(fine_count > 0);
	fine_count--;
	memory_bfree(ctx, fine_blocks[fine_count], sizeof(layermap_list_t));

	trim_ret = layermap_trim_stale_layers_cp(
		active_layer_offset, ctx, trim_now, &outdated_head
	);
	assert(trim_ret == -1);
	assert(outdated_head != NULL);
	assert(ADDR_OF(&outdated_head->next) == NULL);
	assert(ADDR_OF(&outdated_head->layer) == layer2);

	fwmap_t *chain_after_partial = ADDR_OF(active_layer_offset);
	assert(chain_after_partial == layer3);
	fwmap_t *remaining_stale =
		(fwmap_t *)ADDR_OF(&chain_after_partial->next);
	assert(remaining_stale == layer1);
	assert(ADDR_OF(&remaining_stale->next) == NULL);
	assert(count_chain_layers(chain_after_partial) == 2);

	// Recovery: free all remaining blocks and retry. This time both
	// maps' trim work completes.
	for (size_t idx = 0; idx < fine_count; idx++) {
		memory_bfree(ctx, fine_blocks[idx], sizeof(layermap_list_t));
	}
	fine_count = 0;
	for (size_t idx = 0; idx < coarse_count; idx++) {
		memory_bfree(
			ctx, coarse_blocks[idx], ALLOC_FAILURE_COARSE_CHUNK_SIZE
		);
	}
	coarse_count = 0;

	trim_ret = layermap_trim_stale_layers_cp(
		active_layer_offset, ctx, trim_now, &outdated_head
	);
	assert(trim_ret == 0);
	assert(count_chain_layers(ADDR_OF(active_layer_offset)) == 1);

	size_t outdated_count = 0;
	layermap_list_t *cursor = outdated_head;
	while (cursor) {
		outdated_count++;
		cursor = (layermap_list_t *)ADDR_OF(&cursor->next);
	}
	assert(outdated_count == 2);

	// Teardown: free every collected layer and its bookkeeping node,
	// the way the fwstate-map object trim consumers release collected
	// layers.
	cursor = outdated_head;
	while (cursor) {
		layermap_list_t *next =
			(layermap_list_t *)ADDR_OF(&cursor->next);
		fwmap_t *layer = ADDR_OF(&cursor->layer);
		fwmap_free(layer, ctx);
		memory_bfree(ctx, cursor, sizeof(layermap_list_t));
		cursor = next;
	}

	fwmap_t *active_layer = ADDR_OF(active_layer_offset);
	fwmap_free(active_layer, ctx);
	memory_bfree(ctx, active_layer_offset, sizeof(fwmap_t *));

	verify_memory_leaks(ctx, "layermap_alloc_failure_rollback");
	memory_context_fini(ctx);
	fprintf(stderr, "Layermap alloc-failure rollback test PASSED\n");
}

struct rotator_args {
	fwmap_t **active_layer_offset;
	fwmap_config_t *config;
	struct memory_context *ctx;
	volatile bool *stop;
};

static void *
rotator_worker(void *arg) {
	struct rotator_args *args = (struct rotator_args *)arg;
	fprintf(stderr, "Spawn rotating thread\n");
	while (!*args->stop) {
		usleep(200 * 1000);
		now_time++;
		fwmap_t *active_layer = ADDR_OF(args->active_layer_offset);
		if (active_layer) {
			size_t capacity = active_layer->index_mask + 1;
			size_t usage = fwmap_size(active_layer);

			if (usage >= capacity * 0.8) {
				fprintf(stderr,
					"Rotating layers due to capacity: "
					"usage=%zu, "
					"capacity=%zu\n",
					usage,
					capacity);
				layermap_insert_new_layer_cp(
					args->active_layer_offset,
					args->config,
					args->ctx
				);
				fprintf(stderr, "Layer is rotated\n");
			}
		}
	}
	fprintf(stderr, "Rotator thread is exiting\n");
	return NULL;
}

struct worker_args {
	int id;
	fwmap_t **active_layer_offset;
	volatile bool *stop;
};

static void *
put_get_worker(void *arg) {
	struct worker_args *args = (struct worker_args *)arg;
	uint32_t seed = time(NULL) + args->id;

	fprintf(stderr, "Running worker %d\n", args->id);
	uint64_t ops_count = 0;
	while (!*args->stop) {
		if ((++ops_count & 0xfffff) == 0) {
			fprintf(stderr,
				"Worker %d: ops_count=%lu\n",
				args->id,
				ops_count);
		}
		int key = rand_r(&seed) % 1023;
		int value = rand_r(&seed);

		fwmap_t *active_layer = ADDR_OF(args->active_layer_offset);
		rwlock_t *lock = NULL;
		bool value_from_stale = false;
		if (rand_r(&seed) % 2 == 0) {
			layermap_put(
				active_layer,
				args->id,
				now_time,
				now_time + 60,
				&key,
				&value,
				&lock
			);
			if (lock) {
				rwlock_write_unlock(lock);
			}
		} else {
			int *found_value = NULL;
			layermap_get(
				active_layer,
				now_time,
				&key,
				(void **)&found_value,
				&lock,
				&value_from_stale
			);
			if (lock) {
				rwlock_read_unlock(lock);
			}
		}
	}
	fprintf(stderr, "Exiting worker %d\n", args->id);
	fflush(stderr);
	return NULL;
}

void
test_layermap_multithreaded(void *arena) {
	fprintf(stderr, "Testing layermap multithreaded operations...\n");

	const int num_worker_threads = 4;
	const int test_duration_sec = 4;

	struct memory_context *ctx = init_context_from_arena(
		arena, ARENA_SIZE, "layermap_multithreaded"
	);

	fwmap_config_t config = {
		.key_size = sizeof(int),
		.value_size = sizeof(int),
		.hash_seed = 0xdeadbeef,
		.worker_count = num_worker_threads,
		.index_size = 1024,
		.extra_bucket_count = 128,
	};

	// Allocate active_layer pointer in arena (not on stack) because
	// layermap functions and ADDR_OF require it to be in shared memory
	fwmap_t **active_layer_offset = memory_balloc(ctx, sizeof(fwmap_t *));
	assert(active_layer_offset != NULL);

	fwmap_t *first_layer = fwmap_new(&config, ctx);
	assert(first_layer != NULL);
	SET_OFFSET_OF(active_layer_offset, first_layer);

	volatile bool stop_flag = false;

	pthread_t rotator_thread;
	struct rotator_args r_args = {
		active_layer_offset, &config, ctx, &stop_flag
	};
	fprintf(stderr, "Spawning rotating thread\n");
	pthread_create(&rotator_thread, NULL, rotator_worker, &r_args);

	pthread_t worker_threads[num_worker_threads];
	struct worker_args w_args[num_worker_threads];
	for (int i = 0; i < num_worker_threads; i++) {
		fprintf(stderr, "Spawning read/write thread: %d\n", i);
		w_args[i] = (struct worker_args){.id = i,
						 .active_layer_offset =
							 active_layer_offset,
						 .stop = &stop_flag};
		pthread_create(
			&worker_threads[i], NULL, put_get_worker, &w_args[i]
		);
	}

	sleep(test_duration_sec);
	fprintf(stderr, "Stopping threads\n");
	stop_flag = true;

	pthread_join(rotator_thread, NULL);
	for (int i = 0; i < num_worker_threads; i++) {
		pthread_join(worker_threads[i], NULL);
	}

	// Cleanup: destroy all layers in the chain
	fwmap_t *active_layer = ADDR_OF(active_layer_offset);
	fwmap_t *layer = active_layer;
	while (layer) {
		fwmap_t *next = (fwmap_t *)ADDR_OF(&layer->next);
		fwmap_free(layer, ctx);
		layer = next;
	}

	memory_bfree(ctx, active_layer_offset, sizeof(fwmap_t *));

	verify_memory_leaks(ctx, "layermap_multithreaded");
	memory_context_fini(ctx);
	fprintf(stderr, "Layermap multithreaded test PASSED\n");
}

int
main() {
	fprintf(stderr, "=== LayerMap Test Suite ===\n\n");

	void *arena = allocate_locked_memory(ARENA_SIZE);
	if (arena == NULL) {
		return -1;
	}

	test_layermap_basic_operations(arena);
	test_layermap_alloc_failure_rollback(arena);
	test_layermap_multithreaded(arena);

	free_arena(arena, ARENA_SIZE);
	fprintf(stderr, "\n=== All layermap tests PASSED ===\n");
	return 0;
}
