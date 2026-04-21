#pragma once

#include <assert.h>
#include <stdint.h>
#include <string.h>

#include "common/likely.h"

#include "../types/interval_counter.h"

/* Reset the whole ring when all slots are older than the current time. */
static inline int64_t
balancer_ic_try_reset(struct balancer_interval_counter *counter, uint32_t now) {
	int64_t sum = 0;
	if (unlikely(now - counter->last_timestamp >= BALANCER_IC_RING_SIZE)) {
		/*
		 * The entire ring is stale. Sum all remaining deltas so
		 * the caller's running count stays consistent, then clear.
		 */
		for (size_t i = 0; i < BALANCER_IC_RING_SIZE; ++i) {
			sum += counter->diff[i];
		}
		memset(counter->diff, 0, BALANCER_IC_RING_SIZE * sizeof(int32_t)
		);
		counter->last_timestamp = now;
	}
	return sum;
}

/* Expire slots up to `now` and return the net change for the running total. */
static inline int64_t
balancer_ic_advance(struct balancer_interval_counter *counter, uint32_t now) {
	int64_t change = 0;

	/* Sweep past slots: [last_timestamp, now) */
	while (unlikely(counter->last_timestamp < now)) {
		uint32_t idx = counter->last_timestamp & BALANCER_IC_RING_MASK;
		counter->last_timestamp++;
		change += counter->diff[idx];
		counter->diff[idx] = 0;
	}

	/*
	 * Consume the current slot (now). Any +1/-1 written by the
	 * caller for this timestamp is picked up here and the slot is
	 * cleared so subsequent calls at the same `now` start fresh.
	 */
	uint32_t idx = counter->last_timestamp & BALANCER_IC_RING_MASK;
	change += counter->diff[idx];
	counter->diff[idx] = 0;
	return change;
}

/* Start a new interval `[now, until)` and return the change visible at `now`.
 */
static inline int64_t
balancer_ic_make(
	struct balancer_interval_counter *counter, uint32_t now, uint32_t until
) {
	assert(until - now < BALANCER_IC_RING_SIZE);

	int64_t change = balancer_ic_try_reset(counter, now);

	counter->diff[now & BALANCER_IC_RING_MASK] += 1;
	counter->diff[until & BALANCER_IC_RING_MASK] -= 1;

	return change + balancer_ic_advance(counter, now);
}

/* Move an existing interval end from `prev_until` to `new_until`. */
static inline int64_t
balancer_ic_prolong(
	struct balancer_interval_counter *counter,
	uint32_t now,
	uint32_t prev_until,
	uint32_t new_until
) {
	assert(prev_until >= now);
	assert(new_until - now < BALANCER_IC_RING_SIZE);

	int64_t change = balancer_ic_try_reset(counter, now);

	counter->diff[prev_until & BALANCER_IC_RING_MASK] += 1;
	counter->diff[new_until & BALANCER_IC_RING_MASK] -= 1;

	return change + balancer_ic_advance(counter, now);
}