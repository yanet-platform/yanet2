#pragma once

#include "handler/vs.h"

// Counter for the virtual service,
// which depends on the placement of the
// module config in the controlplane topology.
static inline struct vs_stats *
vs_counter(struct vs *vs, size_t worker, struct counter_storage *storage) {
	uint64_t *counter =
		counter_get_address(vs->counter_id, worker, storage);
	return (struct vs_stats *)counter;
}
