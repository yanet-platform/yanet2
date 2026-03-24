#pragma once

#include "interval_counter.h"
#include <stdalign.h>

#define ACTIVE_SESSIONS_TRACKER_MAX_TIMEOUT 100
#define ACTIVE_SESSIONS_TRACKER_PRECISION 16

struct active_sessions_tracker_shard {
	struct rt_interval_counter counter;
	uint32_t count;
	uint32_t last_packet_timestamp;
} __attribute__((aligned(64)));

static inline uint32_t
active_sessions_tracker_now(uint32_t timestamp) {
	return timestamp / ACTIVE_SESSIONS_TRACKER_PRECISION;
}

static inline uint32_t
active_sessions_tracker_until(uint32_t timestamp) {
	return (timestamp + ACTIVE_SESSIONS_TRACKER_PRECISION - 1) /
	       ACTIVE_SESSIONS_TRACKER_PRECISION;
}

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
