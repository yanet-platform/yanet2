#pragma once

#include "common/memory.h"
#include "common/memory_address.h"

#include "../api/vs.h"
#include <stddef.h>
#include <stdint.h>

#include "common/rng.h"
#include "real.h"

////////////////////////////////////////////////////////////////////////////////

#define RING_VALUE_INVALID 0xffffffff

////////////////////////////////////////////////////////////////////////////////

struct ring {
	struct memory_context *mctx;
	size_t len;
	// relative pointer
	uint64_t *ids;
};

static inline int
ring_init(
	struct ring *ring,
	struct memory_context *mctx,
	size_t real_count,
	struct real *reals
) {
	size_t len = 0;
	ring->mctx = mctx;
	for (size_t i = 0; i < real_count; ++i) {
		uint16_t weight =
			(reals[i].flags & BALANCER_REAL_DISABLED_FLAG
				 ? 0
				 : reals[i].weight);
		len += weight;
	}
	uint64_t *ids = memory_balloc(mctx, len * sizeof(uint64_t));
	if (ids == NULL && len > 0) {
		return -1;
	}
	size_t idx = 0;
	for (size_t i = 0; i < real_count; ++i) {
		uint16_t weight =
			(reals[i].flags & BALANCER_REAL_DISABLED_FLAG
				 ? 0
				 : reals[i].weight);
		for (size_t copy = 0; copy < weight; ++copy) {
			ids[idx++] = reals[i].registry_idx;
		}
	}
	uint64_t rng = 0x123131;
	for (size_t i = 1; i < len; ++i) {
		// swap with random before me
		size_t j = rng_next(&rng) % i;
		uint64_t tmp = ids[i];
		ids[i] = ids[j];
		ids[j] = tmp;
	}
	SET_OFFSET_OF(&ring->ids, ids);
	ring->len = len;
	return 0;
}

static inline void
ring_free(struct ring *ring) {
	memory_bfree(
		ring->mctx, ADDR_OF(&ring->ids), ring->len * sizeof(uint64_t)
	);
}

// Selects a real server based on passed index.
static inline uint32_t
ring_get(struct ring *ring, uint64_t index) {
	if (!ring->len) {
		return RING_VALUE_INVALID;
	}
	uint64_t idx = index % ring->len;
	return *(ADDR_OF(&ring->ids) + idx);
}