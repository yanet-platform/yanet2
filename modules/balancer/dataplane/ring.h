#pragma once

#include "common/memory.h"
#include "common/memory_address.h"

#include "../api/vs.h"
#include <stddef.h>
#include <stdint.h>

#include "common/rcu.h"
#include "common/rng.h"
#include "real.h"
#include "worker.h"

#include <lib/dataplane/packet/packet.h>

////////////////////////////////////////////////////////////////////////////////

#define RING_VALUE_INVALID ((uint32_t)-1)

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

////////////////////////////////////////////////////////////////////////////////

struct real_selector_worker {
	uint64_t round_robin_counter;
}; // todo: add alignment to avoid false sharing

struct real_selector {
	struct memory_context *mctx;
	rcu_t rcu;
	struct real_selector_worker workers[MAX_WORKERS_NUM];
	struct ring rings[2];
	_Atomic size_t ring_id;
	int use_prr; // or hash based
};

static inline int
real_selector_init(
	struct real_selector *selector,
	struct memory_context *mctx,
	size_t real_count,
	struct real *reals,
	int use_prr
) {
	selector->mctx = mctx;
	rcu_init(&selector->rcu);
	selector->use_prr = use_prr;
	selector->ring_id = 0;
	if (ring_init(&selector->rings[0], mctx, real_count, reals)) {
		return -1;
	}
	uint64_t rng = 0xdeadbeef;
	for (size_t i = 0; i < MAX_WORKERS_NUM; ++i) {
		selector->workers[i].round_robin_counter = rng_next(&rng);
	}
	return 0;
}

static inline uint32_t
real_selector_select(
	struct real_selector *selector, size_t worker, uint32_t hash
) {
	size_t ring_id =
		RCU_READ_BEGIN(&selector->rcu, worker, &selector->ring_id);
	struct ring *ring = &selector->rings[ring_id];
	size_t idx = selector->use_prr
			     ? selector->workers[worker].round_robin_counter++
			     : hash;
	uint32_t res = ring_get(ring, idx);
	RCU_READ_END(&selector->rcu, worker);
	return res;
}

static inline int
real_selector_update(
	struct real_selector *selector, size_t real_count, struct real *reals
) {
	size_t cur_ring_id = selector->ring_id;
	size_t new_ring_id = cur_ring_id ^ 1;
	struct ring *new_ring = &selector->rings[new_ring_id];
	if (ring_init(new_ring, selector->mctx, real_count, reals)) {
		return -1;
	}
	rcu_update(&selector->rcu, &selector->ring_id, new_ring_id);
	ring_free(&selector->rings[cur_ring_id]);
	return 0;
}