#pragma once

#include "common/memory_address.h"

#include "handler/selector.h"

#include <stddef.h>
#include <stdint.h>

#define SELECTOR_VALUE_INVALID ((uint32_t)-1)

// Selects a real server based on passed index.
static inline uint32_t
ring_get(struct ring *ring, uint64_t index) {
	if (ring->len > 0) {
		uint32_t idx = index % ring->len;
		return *(ADDR_OF(&ring->ids) + idx);
	} else {
		return SELECTOR_VALUE_INVALID;
	}
}

static inline uint32_t
selector_select(struct real_selector *selector, size_t worker, uint32_t hash) {
	size_t ring_id =
		RCU_READ_BEGIN(&selector->rcu, worker, &selector->ring_id);
	struct ring *ring = &selector->rings[ring_id];
	size_t idx = selector->use_rr ? selector->workers[worker].rr_counter++
				      : hash;
	uint32_t res = ring_get(ring, idx);
	RCU_READ_END(&selector->rcu, worker);
	return res;
}