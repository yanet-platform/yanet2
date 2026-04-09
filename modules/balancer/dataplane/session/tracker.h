#pragma once

#include <stdalign.h>

#include "../types/sessions_tracker.h"
#include "interval_counter.h"

/* Convert a packet timestamp to the current tracker tick. */
static inline uint32_t
sessions_tracker_now(uint32_t timestamp) {
	return timestamp / BALANCER_SESSIONS_TRACKER_PRECISION;
}

/* Round a packet timestamp up to the tick where the session expires. */
static inline uint32_t
sessions_tracker_until(uint32_t timestamp) {
	return (timestamp + BALANCER_SESSIONS_TRACKER_PRECISION - 1) /
	       BALANCER_SESSIONS_TRACKER_PRECISION;
}

/* Account for a newly created session on the selected worker shard. */
static inline void
sessions_tracker_new_session(
	struct balancer_sessions_tracker_shard *tracker_shards,
	uint32_t worker_idx,
	uint32_t now,
	uint32_t timeout
) {
	struct balancer_sessions_tracker_shard *shard =
		&tracker_shards[worker_idx];
	shard->count += balancer_ic_make(
		&shard->counter,
		sessions_tracker_now(now),
		sessions_tracker_until(now + timeout)
	);
}

/* Extend an existing session and move its scheduled expiration. */
static inline void
sessions_tracker_prolong_session(
	struct balancer_sessions_tracker_shard *tracker_shards,
	uint32_t worker_idx,
	uint32_t last_packet_timestamp,
	uint32_t prev_timeout,
	uint32_t now,
	uint32_t new_timeout
) {
	struct balancer_sessions_tracker_shard *shard =
		&tracker_shards[worker_idx];
	shard->count += balancer_ic_prolong(
		&shard->counter,
		sessions_tracker_now(now),
		sessions_tracker_until(last_packet_timestamp + prev_timeout),
		sessions_tracker_until(now + new_timeout)
	);
}
