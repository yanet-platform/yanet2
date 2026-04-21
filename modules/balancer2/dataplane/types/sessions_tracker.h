#pragma once

#include <stdalign.h>
#include <stdint.h>

#include "interval_counter.h"

#define BALANCER_SESSIONS_TRACKER_PRECISION 16

/*
 * The underlying rt_interval_counter ring has size R, so the tick
 * distance (until_tick - now_tick) must be < R. With precision P
 * this means the session timeout must satisfy:
 *   (ts + timeout + P-1)/P - ts/P < R
 * This means the session timeout must be at most (R-2) * P + 1.
 * For example, with R=8 (production ring size) and P=16 (production precision),
 * the session timeout must be at most 97s.
 */
#define BALANCER_MAX_SESSION_TIMEOUT                                           \
	((BALANCER_IC_RING_SIZE - 2) * BALANCER_SESSIONS_TRACKER_PRECISION + 1)

static const uint8_t balancer_max_session_timeout =
	BALANCER_MAX_SESSION_TIMEOUT;

/*
 * Per-worker active-session tracker.
 */
struct balancer_sessions_tracker_shard {
	struct balancer_interval_counter counter;
	uint32_t count;
	uint32_t last_timestamp;
} __attribute__((aligned(64)));