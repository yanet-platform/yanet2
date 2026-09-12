#pragma once

#include "lib/controlplane/config/zone.h"

#include "fib.h"

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

	struct route_fib fib;

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
