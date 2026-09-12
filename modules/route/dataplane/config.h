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

// The forwarding table lives in a shared object the module links by name,
// so a table update publishes the object alone.
//
// The handler resolves the link once per batch and reads the table
// through the object.
struct route_module_config {
	struct cp_module cp_module;

	// Where the table sits among the module's object links.
	uint64_t fib_link_idx;

	// Module-level counters, registered by route_module_config_new
	struct route_family_counter_ids counters_v4;
	struct route_family_counter_ids counters_v6;

	// A non-IP packet has no address family, so its drop counter is
	// shared rather than kept in a per-family set.
	uint64_t drop_non_ip_counter_id;
};
