#include "selector.h"

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/rng.h"

#include "lib/controlplane/diag/diag.h"

#include "real.h"
#include "state/state.h"
#include <stdatomic.h>

int
ring_init(
	struct ring *ring,
	struct balancer_state *state,
	struct memory_context *mctx,
	size_t reals_count,
	const struct real *reals
) {
	memset(ring, 0, sizeof(struct ring));
	ring->enabled_len = (reals_count + 7) / 8;
	uint8_t *enabled = memory_balloc(mctx, ring->enabled_len);
	if (enabled == NULL && ring->enabled_len > 0) {
		NEW_ERROR("failed to allocate enabled bits");
		return -1;
	}
	if (ring->enabled_len > 0) {
		memset(enabled, 0, ring->enabled_len);
	}
	size_t len = 0;
	for (size_t i = 0; i < reals_count; ++i) {
		const struct real *real = &reals[i];
		struct real_state *real_state = balancer_state_get_real_by_idx(
			state, real->registry_idx
		);
		uint16_t weight = real_state->enabled ? real_state->weight : 0;
		len += weight;
		if (real_state->enabled) {
			enabled[i / 8] |= 1 << (i % 8);
		}
	}
	uint32_t *ids = memory_balloc(mctx, len * sizeof(uint32_t));
	if (ids == NULL && len > 0) {
		memory_bfree(mctx, enabled, ring->enabled_len);
		NEW_ERROR("failed to allocate weighted reals list");
		return -1;
	}
	size_t idx = 0;
	for (size_t i = 0; i < reals_count; ++i) {
		const struct real *real = &reals[i];
		struct real_state *real_state = balancer_state_get_real_by_idx(
			state, real->registry_idx
		);
		uint16_t weight = real_state->enabled ? real_state->weight : 0;
		for (size_t copy = 0; copy < weight; ++copy) {
			ids[idx++] = i;
		}
	}
	uint64_t rng = 0xdeadbeef;
	for (size_t i = 1; i < len; ++i) {
		// swap with random before me
		size_t j = rng_next(&rng) % i;
		uint32_t tmp = ids[i];
		ids[i] = ids[j];
		ids[j] = tmp;
	}
	SET_OFFSET_OF(&ring->ids, ids);
	SET_OFFSET_OF(&ring->enabled, enabled);
	ring->len = len;
	return 0;
}

static void
ring_free(struct ring *ring, struct memory_context *mctx) {
	memory_bfree(mctx, ADDR_OF(&ring->ids), ring->len * sizeof(uint32_t));
	memory_bfree(mctx, ADDR_OF(&ring->enabled), ring->enabled_len);
}

////////////////////////////////////////////////////////////////////////////////

int
selector_update(
	struct real_selector *selector,
	size_t reals_count,
	const struct real *reals
) {
	struct balancer_state *state = ADDR_OF(&selector->state);
	size_t cur_ring_id = selector->ring_id;
	size_t new_ring_id = cur_ring_id ^ 1;
	struct ring *new_ring = &selector->rings[new_ring_id];
	if (ring_init(new_ring, state, &selector->mctx, reals_count, reals) !=
	    0) {
		PUSH_ERROR("failed to init ring");
		return -1;
	}
	rcu_update(&selector->rcu, &selector->ring_id, new_ring_id);
	ring_free(&selector->rings[cur_ring_id], &selector->mctx);
	return 0;
}

int
selector_init(
	struct real_selector *selector,
	struct balancer_state *state,
	struct memory_context *mctx,
	enum vs_scheduler scheduler
) {
	SET_OFFSET_OF(&selector->state, state);
	memory_context_init_from(&selector->mctx, mctx, "real_selector");
	rcu_init(&selector->rcu);
	selector->use_rr = scheduler == round_robin ? 1 : 0;
	selector->ring_id = 0;
	if (ring_init(&selector->rings[0], state, &selector->mctx, 0, NULL) !=
	    0) {
		PUSH_ERROR("failed to init ring");
		return -1;
	}
	uint64_t rng = 0xdeadbeef;
	for (size_t i = 0; i < MAX_WORKERS_NUM; ++i) {
		selector->workers[i].rr_counter = rng_next(&rng);
	}
	return 0;
}

void
selector_free(struct real_selector *selector) {
	size_t cur_ring_id = selector->ring_id;
	ring_free(&selector->rings[cur_ring_id], &selector->mctx);
}
