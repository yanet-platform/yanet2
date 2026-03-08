#pragma once

#include <stdint.h>

#include "common/network.h"
#include "filter/rule.h"

#include "counters/counters.h"

#define ROUTE_MPLS_TYPE_NONE 0
#define ROUTE_MPLS_TYPE_V4 1
#define ROUTE_MPLS_TYPE_V6 2

struct agent;
struct cp_module;

struct route_mpls_ip4_tunnel {
	uint8_t src[4];
	uint8_t dst[4];
};

struct route_mpls_ip6_tunnel {
	uint8_t src[16];
	uint8_t dst[16];
};

struct route_mpls_nexthop {
	uint16_t kind;
	uint16_t pad;
	union {
		struct route_mpls_ip4_tunnel ip4_tunnel;
		struct route_mpls_ip6_tunnel ip6_tunnel;
	};
	uint32_t mpls_label;
	uint64_t weight;
	char counter[COUNTER_NAME_LEN];
};

struct route_mpls_rule {
	struct filter_net4s net4s;
	struct filter_net6s net6s;

	struct route_mpls_nexthop *nexthops;
	uint64_t nexthop_count;
};

struct cp_module *
route_mpls_module_config_create(struct agent *agent, const char *name);

void
route_mpls_module_config_free(struct cp_module *cp_module);

int
route_mpls_module_config_update(
	struct cp_module *cp_module,
	struct route_mpls_rule *route_mpls_rules,
	uint64_t route_mpls_rule_count
);
