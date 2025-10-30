#pragma once

#include "common/lpm.h"
#include "common/network.h"

#include "controlplane/config/zone.h"

struct route {
	/*
	 * Assuming this is only about directly routed networks there
	 * is nothing to handle except the neighbour ethernet address.
	 */
	struct ether_addr dst_addr;
	struct ether_addr src_addr;
	uint64_t device_id;
};

struct route_list {
	uint64_t start;
	uint64_t count;
};

/*
 * Route module configuration. Handler lookups route list index using
 * corresponding lpm and retrieves start position and count of applicable
 * route indexes. Using packet hash randomization the handler chooses one route
 * index and fetches one route to be applied to a packet.
 */
struct route_module_config {
	struct cp_module cp_module;

	struct lpm lpm_v6;
	struct lpm lpm_v4;

	// All known good routes
	uint64_t route_count;
	struct route *routes;

	// List of route indexes applicable for some destination
	uint64_t route_list_count;
	struct route_list *route_lists;

	// Route indexes storage
	uint64_t route_index_count;
	uint64_t *route_indexes;
};
