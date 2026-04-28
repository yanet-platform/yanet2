#include "selector.h"

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/rng.h"

#include "lib/errors/errors.h"
#include "real.h"
#include <stdatomic.h>

static int
ring_init(
	struct ring *ring,
	struct memory_context *mctx,
	size_t reals_count,
	const struct real *reals,
	yanet_error **err
) {
	memset(ring, 0, sizeof(struct ring));
	ring->enabled_len = (reals_count + 7) / 8;
	uint8_t *enabled = memory_balloc(mctx, ring->enabled_len);
	if (enabled == NULL && ring->enabled_len > 0) {
		yanet_error_add(err, "failed to allocate enabled array");
		return -1;
	}
	if (ring->enabled_len > 0) {
		memset(enabled, 0, ring->enabled_len);
	}
	size_t len = 0;
	for (size_t i = 0; i < reals_count; ++i) {
		const struct real *real = &reals[i];
		uint16_t weight = real->enabled ? real->weight : 0;
		len += weight;
		if (real->enabled) {
			enabled[i / 8] |= 1 << (i % 8);
		}
	}
	uint32_t *ids = memory_balloc(mctx, len * sizeof(uint32_t));
	if (ids == NULL && len > 0) {
		memory_bfree(mctx, enabled, ring->enabled_len);
		yanet_error_add(err, "failed to allocate ids array");
		return -1;
	}
	size_t idx = 0;
	for (size_t i = 0; i < reals_count; ++i) {
		const struct real *real = &reals[i];
		uint16_t weight = real->enabled ? real->weight : 0;
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
	const struct real *reals,
	yanet_error **err
) {
	size_t cur_ring_id = selector->ring_id;
	size_t new_ring_id = cur_ring_id ^ 1;
	struct ring *new_ring = &selector->rings[new_ring_id];
	if (ring_init(new_ring, &selector->mctx, reals_count, reals, err) !=
	    0) {
		yanet_error_add(err, "failed to initialize ring");
		return -1;
	}
	rcu_update(&selector->rcu, &selector->ring_id, new_ring_id);
	ring_free(&selector->rings[cur_ring_id], &selector->mctx);
	return 0;
}

int
selector_init(
	struct real_selector *selector,
	struct memory_context *mctx,
	enum vs_scheduler scheduler,
	yanet_error **err
) {
	memory_context_init_from(&selector->mctx, mctx, "real_selector");
	if (rcu_init(&selector->rcu, &selector->mctx, MAX_WORKERS_NUM) != 0) {
		yanet_error_add(err, "failed to init rcu");
		return -1;
	}
	selector->use_rr = scheduler == round_robin ? 1 : 0;
	selector->ring_id = 0;
	if (ring_init(&selector->rings[0], &selector->mctx, 0, NULL, err) !=
	    0) {
		yanet_error_add(err, "failed to initialize ring");
		rcu_free(&selector->rcu, &selector->mctx);
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
	rcu_free(&selector->rcu, &selector->mctx);
}
