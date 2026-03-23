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

/**
 * Per-instance state of an interval counter.
 *
 * @diff            Offset-encoded pointer to the difference ring buffer
 *                  (int32_t[time_ring_size]). Stored as an offset for
 *                  shared-memory compatibility (see ADDR_OF/SET_OFFSET_OF).
 * @last_timestamp  The most recent timestamp up to which the difference
 *                  array has been swept (consumed). Slots at indices
 *                  <= last_timestamp have already been accounted for in
 *                  the caller's running sum and are zeroed.
 */

struct rt_interval_counter {
	int32_t diff[RT_INTERVAL_COUNTER_RING_SIZE];
	uint32_t last_timestamp;
};

/**
 * Reset the ring buffer if the time gap since the last update is large
 * enough that the entire ring has been logically overwritten.
 *
 * When the gap between `now` and `last_timestamp` is >= time_ring_size,
 * any pending decrements (session endpoints) in the ring have not been
 * swept and would be lost on wraparound. To preserve correctness we sum
 * the entire ring (capturing the net un-swept change), clear it, and
 * reset last_timestamp to `now`.
 *
 * @return The net un-swept delta from the ring (to be added to the
 *         caller's running sum), or 0 if no reset was needed.
 */
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

/**
 * Advance the sweep cursor from last_timestamp up to and including `now`,
 * accumulating and clearing each slot along the way.
 *
 * Design note: the slot at `now` is intentionally consumed and zeroed.
 * Callers (make/prolong) always write their +1 to diff[now] *before*
 * calling advance, so the new contribution is picked up immediately and
 * returned as part of the delta. Zeroing the slot ensures it is not
 * double-counted if another call arrives at the same timestamp.
 *
 * After this function returns, last_timestamp == now and all slots in
 * [old_last_timestamp, now] have been zeroed.
 *
 * @return The accumulated delta from swept slots (to be added to the
 *         caller's running sum).
 */
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

/**
 * Record a new interval [now, now + len) and return the running-sum delta.
 *
 * Writes +1 at `now` (interval starts) and -1 at `now + len` (interval
 * ends), then sweeps forward to `now` to compute the change in active
 * count since the last call.
 *
 * @param now  Current timestamp in seconds.
 * @param len  Duration of the interval in seconds. Must be strictly less
 *             than time_ring_size.
 *
 * @return Delta to add to the caller's running sum of active intervals.
 */
static inline int64_t
rt_interval_counter_make(
	struct rt_interval_counter *counter, uint32_t now, uint32_t len
) {
	assert(len < RT_INTERVAL_COUNTER_RING_SIZE);

	int64_t change = rt_interval_counter_try_reset(counter, now);

	counter->diff[now & RT_INTERVAL_COUNTER_RING_MASK] += 1;
	counter->diff[(now + len) & RT_INTERVAL_COUNTER_RING_MASK] -= 1;

	return change + rt_interval_counter_advance(counter, now);
}

/**
 * Extend an existing interval's endpoint and return the running-sum delta.
 *
 * Cancels the previously recorded -1 at `last_right` and places a new -1
 * at `new_right`, effectively extending the interval's active period.
 * The +1 at `last_right` neutralises the original decrement; the -1 at
 * `new_right` schedules the new one.
 *
 * @param now        Current timestamp in seconds.
 * @param last_right Previous endpoint of the interval. Must be >= now
 *                   (i.e. the interval has not yet expired).
 * @param new_right  New endpoint of the interval. Must satisfy
 *                   new_right - now < time_ring_size.
 *
 * @return Delta to add to the caller's running sum of active intervals.
 */
static inline int64_t
rt_interval_counter_prolong(
	struct rt_interval_counter *counter,
	uint32_t now,
	uint32_t last_right,
	uint32_t new_right
) {
	assert(last_right >= now);
	assert(new_right - now < RT_INTERVAL_COUNTER_RING_SIZE);

	int64_t change = rt_interval_counter_try_reset(counter, now);

	counter->diff[last_right & RT_INTERVAL_COUNTER_RING_MASK] += 1;
	counter->diff[new_right & RT_INTERVAL_COUNTER_RING_MASK] -= 1;

	return change + rt_interval_counter_advance(counter, now);
}