#pragma once

#include "lib/counters/counters.h"

#include "types/real.h"

static inline struct balancer_real_stats *
real_get_stats(
	struct balancer_real *real,
	uint32_t worker,
	struct counter_storage *counter_storage
) {
	return (struct balancer_real_stats *)counter_get_address(
		real->counter_id, worker, counter_storage
	);
}

/* Extract config index (lower 32 bits) from a real's id. */
static inline uint32_t
real_idx_from_id(uint64_t stable_idx) {
	return (uint32_t)stable_idx;
}

static inline bool
real_is_enabled(struct balancer_real *real) {
	return real->flags & balancer_real_enabled;
}

static inline bool
real_is_removed(struct balancer_real *real) {
	return real->flags & balancer_real_removed;
}