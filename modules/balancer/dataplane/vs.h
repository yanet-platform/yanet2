#pragma once

#include "counter.h"
#include "module.h"
#include "ring.h"

#include <threads.h>

#include "../state/registry.h"

////////////////////////////////////////////////////////////////////////////////

typedef uint8_t vs_flags_t;

#define VS_PRESENT_IN_CONFIG_FLAG (1 << 7)

////////////////////////////////////////////////////////////////////////////////

struct virtual_service {
	vs_flags_t flags;

	uint8_t address[16];

	uint16_t port;
	uint8_t proto;

	uint64_t real_count;

	struct lpm src_filter;

	struct ring real_ring;

	uint64_t round_robin_counter;

	uint64_t counter_id;

	// per worker state of the service
	struct service_state *state;
};

////////////////////////////////////////////////////////////////////////////////

static inline vs_counter_t *
vs_counter(
	struct virtual_service *vs,
	size_t worker,
	struct counter_storage *storage
) {
	uint64_t *counter =
		counter_get_address(vs->counter_id, worker, storage);
	return (vs_counter_t *)counter;
}