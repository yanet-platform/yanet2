#pragma once

#include <stddef.h>
#include <stdint.h>

#include "api/real.h"
#include "api/vs.h"
#include "common/network.h"
#include "counters/counters.h"
#include "modules/balancer/dataplane/active_sessions.h"

////////////////////////////////////////////////////////////////////////////////

/**
 * Handler-side view of a real backend server.
 *
 * Represents a single backend server within a virtual service. All fields are
 * const to ensure immutability after initialization. The structure is designed
 * to be compact and cache-friendly for fast-path lookups.
 *
 * Memory Layout:
 * This structure is part of the packet_handler's reals array. Each VS points
 * to a contiguous subset of this array via vs->reals (relative pointer).
 */
struct real {
	// Stable index in the handler's real registry
	// Preserved across config updates for the same real
	const size_t stable_idx;

	// Counter ID for tracking statistics for this real server
	// Registered as "rl_<stable_idx>" in the counter registry
	const uint64_t counter_id;

	// Relative pointer to the
	// array of per-worker sessions tracker
	struct active_sessions_tracker_shard *tracker_shards;

	// Scheduler weight [0..MAX_REAL_WEIGHT]
	uint16_t weight;

	// Source network used for encapsulation/routing to this backend
	// The address is masked by the mask during initialization
	const struct net src;

	// Full identifier of the real server
	const struct real_identifier identifier;

	// Mutable state - preserved from previous config or set from config
	// Whether traffic is allowed to this real. False by default
	bool enabled;

	bool tracker_reused;
};

////////////////////////////////////////////////////////////////////////////////

struct balancer_state;
struct counter_registry;
struct packet_handler;

int
real_init(
	struct real *real,
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	struct vs_identifier *vs,
	struct named_real_config *named_config,
	struct counter_registry *registry,
	size_t workers,
	struct memory_context *mctx
);

void
real_free(struct real *real, size_t workers, struct memory_context *mctx);

/**
 * Resolve real registry index from a counter handle.
 * Returns index on success, or -1 on error.
 */
ssize_t
counter_to_real_registry_idx(struct counter_handle *counter);