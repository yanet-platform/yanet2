#pragma once

#include <stddef.h>
#include <stdint.h>

#include "api/real.h"
#include "api/vs.h"
#include "common/network.h"
#include "counters/counters.h"

////////////////////////////////////////////////////////////////////////////////

struct real {
	const struct net src; // Source network used for encapsulation/routing
	const struct relative_real_identifier
		identifier;	   // Identifier of the real (dst
				   // address + vs identifier)
	const size_t registry_idx; // Index in the registry
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