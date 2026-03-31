#pragma once

#include "lib/counters/counters.h"
#include "common/memory_address.h"

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
	return counter_get_address(
		ADDR_OF(&vs->rule_counters)[rule_idx], worker, counter_storage
	);
}

static inline struct balancer_vs *
packet_handler_get_vs(
	struct balancer_packet_handler *packet_handler, size_t id
) {
	return (struct balancer_vs *)big_array_get(
		&packet_handler->vs, id * sizeof(struct balancer_vs)
	);
}