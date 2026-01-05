#pragma once

#include "api/real.h"

////////////////////////////////////////////////////////////////////////////////

/**
 * State-layer representation of a real backend.
 */
struct real_state {
	struct real_identifier identifier;

	// Whether traffic is allowed to this real
	bool enabled;

	uint16_t weight;

	// index of the real in registry, used to track counters
	size_t registry_idx;

	// index of the virtual service in the registry,
	// used to track counters
	size_t vs_registry_idx;
};
