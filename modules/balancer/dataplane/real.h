#pragma once

#include "../state/registry.h"
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

typedef uint8_t real_flags_t;

////////////////////////////////////////////////////////////////////////////////

struct real {
	// index in the balancer registry
	size_t idx;

	real_flags_t flags;
	uint16_t weight;
	uint8_t dst_addr[16];
	uint8_t src_addr[16];
	uint8_t src_mask[16];

	// per worker state information
	struct service_state *state;
};