#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "ring.h"

#define PDUMP_NS_PER_SEC 1000000000ULL
#define PDUMP_MAX_BURST 32ULL

struct pdump_rate {
	uint64_t rate_pps;
	uint64_t threshold;
	uint64_t cap;
};

static inline struct pdump_rate
pdump_rate_init(uint64_t rate_pps, uint64_t worker_count) {
	struct pdump_rate rate = {.rate_pps = rate_pps};
	if (rate_pps == 0 || worker_count == 0) {
		return rate;
	}
	rate.threshold = PDUMP_NS_PER_SEC * worker_count;
	uint64_t burst = PDUMP_MAX_BURST;
	if (burst > UINT64_MAX / rate.threshold) {
		burst = UINT64_MAX / rate.threshold;
	}
	rate.cap = rate.threshold * burst;
	return rate;
}

static inline bool
pdump_rate_allow(
	struct ring_buffer *ring, uint64_t now, const struct pdump_rate *rate
) {
	if (rate->rate_pps == 0) {
		return true;
	}
	if (rate->threshold == 0) {
		return false;
	}

	if (!ring->rate_initialized) {
		ring->rate_last_time = now;
		ring->rate_credit = rate->threshold;
		ring->rate_initialized = 1;
	}

	uint64_t elapsed =
		now > ring->rate_last_time ? now - ring->rate_last_time : 0;
	ring->rate_last_time = now;
	if (elapsed != 0) {
		uint64_t remaining = rate->cap - ring->rate_credit;
		if (remaining == 0 ||
		    elapsed > (remaining - 1) / rate->rate_pps) {
			ring->rate_credit = rate->cap;
		} else {
			ring->rate_credit += elapsed * rate->rate_pps;
		}
	}
	if (ring->rate_credit < rate->threshold) {
		return false;
	}
	ring->rate_credit -= rate->threshold;
	return true;
}
