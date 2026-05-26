#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/flatmap.h"
#include "filter/filter.h"

#include "selector.h"

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