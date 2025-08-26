#pragma once

#include "common/exp_array.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include <assert.h>
#include <stdint.h>

struct ring {
	struct memory_context *memory_context;
	uint64_t cap;
	uint64_t len;
	uint32_t *ids; // array with repeated real indices

	uint64_t weight_count;
	uint16_t *weights;
};

#define RING_VALUE_INVALID 0xffffffff

/**
 * Fills the ids array with repeated real indices based on their weights.
 * Example: weights [2, 1, 3] -> ids [0, 0, 1, 2, 2, 2]
 */
static void
ring_fill_ids(struct ring *ring, uint32_t *ids) {
	uint16_t *weights = ADDR_OF(&ring->weights);
	uint64_t idx = 0;
	for (uint64_t real_idx = 0; real_idx < ring->weight_count; real_idx++) {
		for (uint16_t count = 0; count < weights[real_idx]; count++) {
			ids[idx] = real_idx;
			idx++;
		}
	}
}

static inline int
ring_init(
	struct ring *ring,
	struct memory_context *memory_context,
	uint64_t real_count
) {
	SET_OFFSET_OF(&ring->memory_context, memory_context);
	ring->weight_count = real_count;

	uint16_t *weights = (uint16_t *)memory_balloc(
		memory_context, real_count * sizeof(uint16_t)
	);
	if (real_count && !weights) {
		errno = ENOMEM;
		return -1;
	}
	memset(weights, 0, real_count * sizeof(uint16_t));

	SET_OFFSET_OF(&ring->weights, weights);
	ring->len = 0;
	ring->cap = 0;
	ring->ids = NULL;
	return 0;
}

/**
 * Updates the weight of a specific real server.
 * Reallocates ids array if new total weight exceeds capacity.
 */
static inline int
ring_change_weight(struct ring *ring, uint32_t real_idx, uint16_t new_weight) {
	assert(real_idx < ring->weight_count);

	uint16_t *weight_ptr = ADDR_OF(&ring->weights) + real_idx;

	if (*weight_ptr == new_weight) {
		return 0;
	}

	uint32_t new_len = ring->len - *weight_ptr + new_weight;
	if (new_len < ring->cap) {
		*weight_ptr = new_weight;
		ring_fill_ids(ring, ADDR_OF(&ring->ids));
		ring->len = new_len;
		return 0;
	}

	struct memory_context *memory_context = ADDR_OF(&ring->memory_context);
	uint64_t new_cap;
	uint32_t *ids = (uint32_t *)mem_array_alloc_exp(
		memory_context, sizeof(uint32_t), new_len, &new_cap
	);
	if (!ids) {
		errno = ENOMEM;
		return -1;
	}

	uint32_t *old_ids = ADDR_OF(&ring->ids);
	uint64_t old_len = ring->len;

	*weight_ptr = new_weight;
	ring_fill_ids(ring, ids);
	SET_OFFSET_OF(&ring->ids, ids);
	ring->len = new_len;
	ring->cap = new_cap;

	mem_array_free_exp(memory_context, old_ids, sizeof(uint32_t), old_len);
	return 0;
}

/**
 * Selects a real server based on weighted random selection.
 * Caller must ensure rnd changes on each call for proper distribution.
 */
static inline uint32_t
ring_get(struct ring *ring, uint64_t rnd) {
	if (!ring->len) {
		return RING_VALUE_INVALID;
	}
	uint64_t idx = rnd % ring->len;
	return *(ADDR_OF(&ring->ids) + idx);
}

static inline void
ring_free(struct ring *ring) {
	struct memory_context *memory_context = ADDR_OF(&ring->memory_context);

	uint32_t *ids = ADDR_OF(&ring->ids);
	memory_bfree(memory_context, ids, ring->cap * sizeof(uint32_t));

	uint16_t *weights = ADDR_OF(&ring->weights);
	memory_bfree(
		memory_context, weights, ring->weight_count * sizeof(uint16_t)
	);
}
