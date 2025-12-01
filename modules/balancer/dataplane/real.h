#pragma once

#include "counters/counters.h"
#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

typedef uint8_t real_flags_t;

#define REAL_PRESENT_IN_CONFIG_FLAG (1 << 7)

////////////////////////////////////////////////////////////////////////////////

// Represents real as part of the virtual service
struct real {
	// index in the balancer registry
	size_t registry_idx;

	real_flags_t flags;
	uint16_t weight;
	uint8_t dst_addr[16];
	uint8_t src_addr[16];
	uint8_t src_mask[16];

	uint64_t counter_id;

	// per worker state information
	struct service_state *state;
};

////////////////////////////////////////////////////////////////////////////////

static inline struct balancer_real_stats *
real_counter(
	struct real *real, size_t worker, struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(real->counter_id, worker, storage);
	return (struct balancer_real_stats *)counter;
}