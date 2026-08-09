// Verifies that layermap_trim_stale_layers_cp does not orphan a stale layer
// when its recording-node allocation fails mid-trim.
//
// Before the fix, the function unlinked a stale layer and only then tried to
// allocate the layermap_list_t node that records it; on OOM it returned -1
// with the layer already unlinked but never recorded, permanently leaking
// it. The fix re-links the layer into the chain before returning -1 so the
// next trim retries it. This test exhausts the arena to force the OOM, then
// frees every still-reachable object (the recording list plus the surviving
// chain); a balanced alloc/free count proves nothing was orphaned.

#include "lib/fwstate/layermap.h"
#include "test_utils.h"

#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#define ARENA_SIZE (1 << 20) // 1 MiB

// Count layers reachable from a head offset by walking the ->next chain.
static unsigned
count_chain(fwmap_t **head_off) {
	unsigned count = 0;
	for (fwmap_t *layer = ADDR_OF(head_off); layer != NULL;
	     layer = (fwmap_t *)ADDR_OF(&layer->next)) {
		count++;
	}
	return count;
}

// Free every layermap_list_t node in the list and the fwmap layer it
// references (mirrors fwstate_map_outdated_layers_free on the success path,
// after the RCU barrier).
static void
free_outdated_list(layermap_list_t *list, struct memory_context *ctx) {
	layermap_list_t *node = list;
	while (node) {
		fwmap_t *layer = ADDR_OF(&node->layer);
		layermap_list_t *next = (layermap_list_t *)ADDR_OF(&node->next);
		fwmap_free(layer, ctx);
		memory_bfree(ctx, node, sizeof(*node));
		node = next;
	}
}

// Free every layer reachable from a head offset, then the head offset slot.
static void
free_chain(fwmap_t **head_off, struct memory_context *ctx) {
	fwmap_t *layer = ADDR_OF(head_off);
	while (layer) {
		fwmap_t *next = (fwmap_t *)ADDR_OF(&layer->next);
		fwmap_free(layer, ctx);
		layer = next;
	}
	memory_bfree(ctx, head_off, sizeof(fwmap_t *));
}

// Build a chain of `extra` stale layers stacked on a base layer, returning
// the arena-resident head offset slot. Fresh fwmaps report max_deadline == 0,
// so every layer below the head is outdated for any now > 0.
static fwmap_t **
build_stale_chain(struct memory_context *ctx, unsigned extra) {
	fwmap_config_t config = {
		.key_size = sizeof(int),
		.value_size = sizeof(int),
		.hash_seed = 0,
		.worker_count = 1,
		.index_size = 128,
		.extra_bucket_count = 16,
	};

	fwmap_t **head_off = memory_balloc(ctx, sizeof(fwmap_t *));
	assert(head_off != NULL);
	fwmap_t *base = fwmap_new(&config, ctx);
	assert(base != NULL);
	SET_OFFSET_OF(head_off, base);

	for (unsigned i = 0; i < extra; i++) {
		assert(layermap_insert_new_layer_cp(head_off, &config, ctx) == 0
		);
	}
	return head_off;
}

// Exhaust the allocator and then release `spare` of the most recently
// allocated filler blocks, so at most `spare` list-node allocations can
// succeed during the subsequent trim.
//
// Releasing the most recently allocated blocks (highest indices first)
// leaves each freed block's buddy still allocated, which prevents the buddy
// allocator from coalescing them into a larger block; each freed slot thus
// enables exactly one same-size allocation.
static void
exhaust_memory(
	struct memory_context *ctx,
	unsigned spare,
	void ***out_fillers,
	size_t *out_count
) {
	size_t cap = 65536;
	void **fillers = malloc(cap * sizeof(*fillers));
	assert(fillers != NULL);
	size_t count = 0;
	while (count < cap) {
		void *block = memory_balloc(ctx, sizeof(layermap_list_t));
		if (block == NULL) {
			break;
		}
		fillers[count++] = block;
	}
	for (unsigned i = 0; i < spare && count > 0; i++) {
		memory_bfree(ctx, fillers[--count], sizeof(layermap_list_t));
	}
	*out_fillers = fillers;
	*out_count = count;
}

// Drive one trim-under-OOM scenario with `extra` stale layers and room for
// `spare` recording nodes. With spare < extra the OOM reliably fires
// mid-trim; the rolled-back layer must stay in the chain so freeing every
// reachable object leaves a balanced alloc/free count.
static void
run_trim_oom(void *arena, unsigned extra, unsigned spare) {
	assert(spare < extra);

	struct memory_context *ctx =
		init_context_from_arena(arena, ARENA_SIZE, "trim_oom");

	fwmap_t **head_off = build_stale_chain(ctx, extra);
	assert(count_chain(head_off) == extra + 1);

	void **fillers;
	size_t filler_count;
	exhaust_memory(ctx, spare, &fillers, &filler_count);

	layermap_list_t *outdated = NULL;
	int ret = layermap_trim_stale_layers_cp(
		head_off, ctx, /*now=*/1, &outdated
	);
	// spare < extra guarantees the trim runs out of recording nodes before
	// reaching the end of the stale chain.
	assert(ret == -1);

	// Whatever was recorded before the failure, plus whatever survived in
	// the chain, must together account for every allocated layer.
	free_outdated_list(outdated, ctx);
	free_chain(head_off, ctx);

	for (size_t i = 0; i < filler_count; i++) {
		memory_bfree(ctx, fillers[i], sizeof(layermap_list_t));
	}
	free(fillers);

	verify_memory_leaks(ctx, "trim_oom");
	memory_context_fini(ctx);
}

// With the arena fully exhausted, the first recording-node allocation fails.
// The fix re-links that layer, so the whole chain survives intact and no
// layer is orphaned.
void
test_trim_oom_first_alloc(void *arena) {
	fprintf(stderr, "Testing trim OOM on first recording-node alloc...\n");
	run_trim_oom(arena, /*extra=*/3, /*spare=*/0);
	fprintf(stderr, "Trim OOM (first alloc) test PASSED\n");
}

// With room for one or two recording nodes, the first stale layers are
// unlinked and recorded before a later one fails and is rolled back.
// Freeing both the recorded list and the surviving chain must balance.
void
test_trim_oom_mid_trim(void *arena) {
	fprintf(stderr, "Testing trim OOM mid-trim...\n");
	run_trim_oom(arena, /*extra=*/3, /*spare=*/1);
	run_trim_oom(arena, /*extra=*/3, /*spare=*/2);
	fprintf(stderr, "Trim OOM (mid-trim) test PASSED\n");
}

int
main(void) {
	fprintf(stderr, "=== LayerMap trim-OOM leak test ===\n\n");

	void *arena = allocate_locked_memory(ARENA_SIZE);
	if (arena == NULL) {
		return -1;
	}

	test_trim_oom_first_alloc(arena);
	test_trim_oom_mid_trim(arena);

	free_arena(arena, ARENA_SIZE);
	fprintf(stderr, "\n=== All trim-OOM tests PASSED ===\n");
	return 0;
}
