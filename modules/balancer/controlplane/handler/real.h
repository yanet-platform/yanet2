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

	// Identifier of the real server (destination address + VS identifier)
	// Uses relative_real_identifier which contains the backend's address
	const struct relative_real_identifier identifier;

	// Index in the balancer state's real registry
	// Used to look up real_state for weight and enabled status
	const size_t registry_idx;

	// Counter ID for tracking statistics for this real server
	// Registered as "rl_<registry_idx>" in the counter registry
	const uint64_t counter_id;
};

////////////////////////////////////////////////////////////////////////////////

struct balancer_state;
struct counter_registry;

/**
 * Initialize a real view for the given packet handler index.
 *
 * Returns 0 on success, -1 on error.
 */
int
real_init(
	struct real *real,
	struct balancer_state *state,
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