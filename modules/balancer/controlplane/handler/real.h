#pragma once

#include <stddef.h>
#include <stdint.h>

#include "api/real.h"
#include "common/network.h"
#include "counters/counters.h"

////////////////////////////////////////////////////////////////////////////////

struct real_state;

/**
 * Lightweight view of a real used by the packet handler.
 */
struct real {
	uint16_t weight;     // Effective scheduler weight
	struct net src;	     // Source network used for encapsulation/routing
	struct real_identifier identifier; // Identifier of the real (dst address + vs identifier)
	size_t registry_idx; // Index in the registry
	uint64_t counter_id;
	bool enabled;
};

/**
 * Return effective weight of the real.
 */
uint16_t
real_weight(struct real *real);

////////////////////////////////////////////////////////////////////////////////

struct balancer_state;
struct named_real_config;
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
	struct named_real_config *config,
	struct counter_registry *registry
);

/**
 * Resolve real registry index from a counter handle.
 * Returns index on success, or -1 on error.
 */
ssize_t
counter_to_real_registry_idx(struct counter_handle *counter);