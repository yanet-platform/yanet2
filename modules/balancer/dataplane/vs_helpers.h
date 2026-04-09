#pragma once

#include "common/memory_address.h"
#include "lib/counters/counters.h"

#include "dataplane.h"
#include "types/vs.h"

static inline struct balancer_vs_stats *
vs_get_stats(
	struct balancer_vs *vs,
	uint32_t worker,
	struct counter_storage *counter_storage
) {
	return (struct balancer_vs_stats *)counter_get_address(
		vs->counter_id, worker, counter_storage
	);
}

static inline uint64_t *
vs_get_acl_stats(
	struct balancer_vs *vs,
	uint32_t worker,
	struct counter_storage *counter_storage,
	uint32_t rule_idx
) {
	// Rule counter is undefined if tag is empty
	uint64_t id = ADDR_OF(&vs->rule_counter_ids)[rule_idx];
	return id != (uint64_t)-1
		       ? counter_get_address(
				 ADDR_OF(&vs->rule_counter_ids)[rule_idx],
				 worker,
				 counter_storage
			 )
		       : NULL;
}
