#pragma once

/**
 * @file interval_counter.h
 *
 * Real-time interval counter using a circular difference array.
 *
 * Tracks the number of concurrently active intervals (e.g. sessions) over
 * a sliding time window. Each interval is defined by a start timestamp and
 * a duration in seconds.
 *
 * The data structure uses a difference array technique on a power-of-two
 * sized ring buffer. When an interval [T, T+len) is created, we record
 * +1 at position T and -1 at position T+len. The prefix sum of the
 * difference array at any point in time gives the number of currently
 * active intervals.
 *
 * The caller is responsible for maintaining a running sum (int64_t) of
 * active intervals. Each call to make/prolong returns a delta that must
 * be added to this running sum.
 *
 * Preconditions (caller must guarantee):
 *   - All interval lengths (len, new_right - now) are strictly less than
 *     time_ring_size.
 *   - In prolong(), last_right >= now (the old endpoint has not passed).
 *   - time_ring_size is a power of two.
 *   - time_ring_size_mask == time_ring_size - 1.
 *
 * Typical usage:
 *
 *   int64_t active_sessions = 0;
 *
 *   // New session arrives with estimated duration `len` seconds
 *   active_sessions += rt_interval_counter_make(&ctr, &cfg, now, len);
 *
 *   // Existing session is extended from old endpoint to new endpoint
 *   active_sessions += rt_interval_counter_prolong(&ctr, &cfg, now,
 *                                                  old_end, new_end);
 */

#include "common/likely.h"
#include <assert.h>
#include <stdint.h>
#include <string.h>

#define RT_INTERVAL_COUNTER_RING_SIZE_EXP 3u
#define RT_INTERVAL_COUNTER_RING_SIZE (1u << RT_INTERVAL_COUNTER_RING_SIZE_EXP)
#define RT_INTERVAL_COUNTER_RING_MASK (RT_INTERVAL_COUNTER_RING_SIZE - 1u)

/*
 * Ring-based interval counter that stores per-timestamp deltas.
 *
 * The caller keeps a running total and applies the returned change.
 * `make` starts an interval at `now` and schedules its end at `until`.
 * `prolong` moves a previously scheduled end further in time.
 */
struct rt_interval_counter {
	int32_t diff[RT_INTERVAL_COUNTER_RING_SIZE];
	uint32_t last_timestamp;
};

/* Reset the whole ring when all slots are older than the current time. */
static inline int64_t
rt_interval_counter_try_reset(
	struct rt_interval_counter *counter, uint32_t now
) {
	int64_t sum = 0;
	if (unlikely(
		    now - counter->last_timestamp >=
		    RT_INTERVAL_COUNTER_RING_SIZE
	    )) {
		/*
		 * The entire ring is stale. Sum all remaining deltas so
		 * the caller's running count stays consistent, then clear.
		 */
		for (size_t i = 0; i < RT_INTERVAL_COUNTER_RING_SIZE; ++i) {
			sum += counter->diff[i];
		}
		memset(counter->diff,
		       0,
		       RT_INTERVAL_COUNTER_RING_SIZE * sizeof(int32_t));
		counter->last_timestamp = now;
	}
	return sum;
}

/* Expire slots up to `now` and return the net change for the running total. */
static inline int64_t
rt_interval_counter_advance(struct rt_interval_counter *counter, uint32_t now) {
	int64_t change = 0;

	/* Sweep past slots: [last_timestamp, now) */
	while (unlikely(counter->last_timestamp < now)) {
		uint32_t idx =
			counter->last_timestamp & RT_INTERVAL_COUNTER_RING_MASK;
		counter->last_timestamp++;
		change += counter->diff[idx];
		counter->diff[idx] = 0;
	}

	/*
	 * Consume the current slot (now). Any +1/-1 written by the
	 * caller for this timestamp is picked up here and the slot is
	 * cleared so subsequent calls at the same `now` start fresh.
	 */
	uint32_t idx = counter->last_timestamp & RT_INTERVAL_COUNTER_RING_MASK;
	change += counter->diff[idx];
	counter->diff[idx] = 0;
	return change;
}

/* Start a new interval `[now, until)` and return the change visible at `now`.
 */
static inline int64_t
rt_interval_counter_make(
	struct rt_interval_counter *counter, uint32_t now, uint32_t until
) {
	assert(until - now < RT_INTERVAL_COUNTER_RING_SIZE);

	int64_t change = rt_interval_counter_try_reset(counter, now);

	counter->diff[now & RT_INTERVAL_COUNTER_RING_MASK] += 1;
	counter->diff[until & RT_INTERVAL_COUNTER_RING_MASK] -= 1;

	return change + rt_interval_counter_advance(counter, now);
}

/* Move an existing interval end from `prev_until` to `new_until`. */
static inline int64_t
rt_interval_counter_prolong(
	struct rt_interval_counter *counter,
	uint32_t now,
	uint32_t prev_until,
	uint32_t new_until
) {
	assert(prev_until >= now);
	assert(new_until - now < RT_INTERVAL_COUNTER_RING_SIZE);

	int64_t change = rt_interval_counter_try_reset(counter, now);

	counter->diff[prev_until & RT_INTERVAL_COUNTER_RING_MASK] += 1;
	counter->diff[new_until & RT_INTERVAL_COUNTER_RING_MASK] -= 1;

	return change + rt_interval_counter_advance(counter, now);
}