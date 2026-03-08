#pragma once

#include "controlplane/config/cp_module.h"

#include "filter/filter.h"

#define ROUTE_TYPE_NONE 0
#define ROUTE_TYPE_V4 1
#define ROUTE_TYPE_V6 2

struct ip4_tunnel {
	uint8_t src[4];
	uint8_t dst[4];
};

struct ip6_tunnel {
	uint8_t src[16];
	uint8_t dst[16];
};

struct nexthop {
	uint16_t type;
	uint16_t pad;
	uint32_t mpls_label;
	union {
		struct ip4_tunnel ip4_tunnel;
		struct ip6_tunnel ip6_tunnel;
	};
	uint64_t counter_id;
};

struct target {
	struct nexthop *nexthops;
	uint64_t nexthop_count;
	uint64_t nexthop_map_size;
	uint64_t nexthop_map[];
};

/*
 * Route module configuration. Handler lookups route list index using
 * corresponding lpm and retrieves start position and count of applicable
 * route indexes. Using packet hash randomization the handler chooses one route
 * index and fetches one route to be applied to a packet.
 */
struct module_config {
	struct cp_module cp_module;

	struct target **targets;
	uint64_t target_count;

	struct filter filter_ip4;
	struct filter filter_ip6;
};
