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

	// Per-nexthop packet/byte counter, or COUNTER_INVALID if this
	// nexthop is not individually counted.
	uint64_t counter_id;
};

struct route_list {
	uint64_t start;
	uint64_t count;
};

// Counter ids of a single address family.
//
// The ethertype test already selects a family per packet, so the handler
// points at the matching set there and the shared tail stays family agnostic.
// The sets live in the config so that selecting one costs a pointer and the
// handler builds nothing per invocation.
struct route_family_counter_ids {
	uint64_t forwarded;
	uint64_t drop_no_route;
	uint64_t drop_ttl_expired;
	uint64_t drop_empty_route_list;
	uint64_t drop_device_unresolved;
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

	// Module-level counters, registered by route_module_config_new
	struct route_family_counter_ids counters_v4;
	struct route_family_counter_ids counters_v6;

	// A non-IP packet has no address family, so its drop counter is
	// shared rather than kept in a per-family set.
	uint64_t drop_non_ip_counter_id;

	// Index of the per-route "routes" counter registry within
	// cp_module.runtime_counter_registries. Each per-route counter_id is
	// resolved against this registry's per-worker storage.
	uint64_t routes_registry_idx;
};
