#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/flatmap.h"
#include "filter/filter.h"

#include "selector.h"

#include "lib/counters/counters.h"

enum vs_flags {
	vs_fix_mss = 1u << 0,
	vs_ops = 1u << 1,
	vs_gre = 1u << 2,
	vs_ip6 = 1u << 3
};

struct virtual_service {
	struct real *reals;
	uint32_t reals_count;

	struct flatmap reals_map;

	uint64_t counter_id;

	struct filter acl;

	uint64_t *rule_counter_ids;
	size_t rule_count;

	uint8_t flags;

	struct real_selector *selector;
};

static inline struct balancer_vs_stats *
vs_fetch_stats(
	struct virtual_service *vs,
	uint32_t worker,
	struct counter_storage *counter_storage
) {
	return (struct balancer_vs_stats *)counter_get_address(
		vs->counter_id, worker, counter_storage
	);
}

static inline uint64_t *
vs_fetch_acl_stats(
	struct virtual_service *vs,
	uint32_t worker,
	struct counter_storage *counter_storage,
	uint32_t rule_idx
) {
	// Rule counter is undefined if tag is empty
	uint64_t id = ADDR_OF(&vs->rule_counter_ids)[rule_idx];
	return id != (uint64_t)-1
		       ? counter_get_address(id, worker, counter_storage)
		       : NULL;
}
