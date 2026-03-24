#pragma once

#include "interval_counter.h"
#include <stdalign.h>

/*
 * The underlying rt_interval_counter ring has size 8, so the tick
 * distance (until_tick - now_tick) must be < 8.  With precision=16
 * this means the session timeout must satisfy:
 *   (ts + timeout + 15)/16 - ts/16 < 8
 * For ts=0: (timeout + 15)/16 < 8  =>  timeout < 113.
 * Safe timeouts: 16, 32, 48, 64, 80, 96, 112.
 */

#define ACTIVE_SESSIONS_TRACKER_MAX_TIMEOUT 100
#define ACTIVE_SESSIONS_TRACKER_PRECISION 16

/*
 * Per-worker active-session tracker.
 *
 * Session lifetimes are rounded to `ACTIVE_SESSIONS_TRACKER_PRECISION`
 * ticks and accumulated through [`struct
 * rt_interval_counter`](modules/balancer/dataplane/interval_counter.h:19).
 */
struct active_sessions_tracker_shard {
	struct rt_interval_counter counter;
	uint32_t count;
	uint32_t last_packet_timestamp;
} __attribute__((aligned(64)));

/* Convert a packet timestamp to the current tracker tick. */
static inline uint32_t
active_sessions_tracker_now(uint32_t timestamp) {
	return timestamp / ACTIVE_SESSIONS_TRACKER_PRECISION;
}

/* Round a packet timestamp up to the tick where the session expires. */
static inline uint32_t
active_sessions_tracker_until(uint32_t timestamp) {
	return (timestamp + ACTIVE_SESSIONS_TRACKER_PRECISION - 1) /
	       ACTIVE_SESSIONS_TRACKER_PRECISION;
}

/* Account for a newly created session on the selected worker shard. */
static inline void
active_sessions_tracker_new_session(
	struct active_sessions_tracker_shard *tracker_shards,
	uint32_t worker_idx,
	uint32_t now,
	uint32_t timeout
) {
	struct active_sessions_tracker_shard *shard =
		&tracker_shards[worker_idx];
	shard->count += rt_interval_counter_make(
		&shard->counter,
		active_sessions_tracker_now(now),
		active_sessions_tracker_until(now + timeout)
	);
	shard->last_packet_timestamp = now;
}

/* Extend an existing session and move its scheduled expiration. */
static inline void
active_sessions_tracker_prolong_session(
	struct active_sessions_tracker_shard *tracker_shards,
	uint32_t worker_idx,
	uint32_t last_packet_timestamp,
	uint32_t prev_timeout,
	uint32_t now,
	uint32_t new_timeout
) {
	struct active_sessions_tracker_shard *shard =
		&tracker_shards[worker_idx];
	shard->count += rt_interval_counter_prolong(
		&shard->counter,
		active_sessions_tracker_now(now),
		active_sessions_tracker_until(
			last_packet_timestamp + prev_timeout
		),
		active_sessions_tracker_until(now + new_timeout)
	);
	shard->last_packet_timestamp = now;
}
