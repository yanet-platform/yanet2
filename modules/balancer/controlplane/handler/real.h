#pragma once

#include <stddef.h>
#include <stdint.h>

#include "api/real.h"
#include "api/vs.h"
#include "common/network.h"
#include "counters/counters.h"

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
	// Source network used for encapsulation/routing to this backend
	// The address is masked by the mask during initialization
	const struct net src;

	// Full identifier of the real server
	const struct real_identifier identifier;

	// Stable index in the handler's real registry
	// Preserved across config updates for the same real
	const size_t stable_idx;

	// Counter ID for tracking statistics for this real server
	// Registered as "rl_<stable_idx>" in the counter registry
	const uint64_t counter_id;

	// Mutable state - preserved from previous config or set from config
	// Whether traffic is allowed to this real. False by default
	bool enabled;

	// Scheduler weight [0..MAX_REAL_WEIGHT]
	uint16_t weight;
};

////////////////////////////////////////////////////////////////////////////////

struct balancer_state;
struct counter_registry;
struct packet_handler;

/**
 * Initialize a real view for the given packet handler index.
 *
 * Looks up or inserts the real in the handler's registry, assigns a stable
 * index, and preserves enabled/weight state from prev_handler if the real
 * existed before.
 *
 * Returns 0 on success, -1 on error.
 */
int
real_init(
	struct real *real,
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	struct vs_identifier *vs,
	struct named_real_config *config,
	struct counter_registry *registry
);

/**
 * Resolve real registry index from a counter handle.
 * Returns index on success, or -1 on error.
 */
ssize_t
counter_to_real_registry_idx(struct counter_handle *counter);