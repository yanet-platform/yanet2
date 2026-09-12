#pragma once

#include "common/lpm.h"
#include "common/network.h"

#include "lib/controlplane/config/cp_object.h"

// Object type under which a FIB is registered in a configuration
// generation and linked by a route module config.
#define ROUTE_FIB_OBJECT_TYPE "route_fib"

struct route {
	/*
	 * Assuming this is only about directly routed networks there
	 * is nothing to handle except the neighbour ethernet address.
	 */
	struct ether_addr dst_addr;
	struct ether_addr src_addr;
	// Index into the device links of the module that runs the FIB.
	uint64_t device_id;

	// Per-nexthop packet/byte counter, or COUNTER_INVALID if this
	// nexthop is not individually counted.
	uint64_t counter_id;
};

struct route_list {
	uint64_t start;
	uint64_t count;
};

// A forwarding table: two LPM trees mapping a destination to a route list,
// and the nexthops the lists select from by packet hash.
//
// The table is plain data. A route names its device by an index into the
// device links of the module that runs the table and its counter by an id
// in the registry the owner chose, so the same layout serves both a module
// config holding its own table and a shared object linked by name.
struct route_fib {
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

// A FIB published as a shared object, registered under its type and name
// and reachable from a linking module's execution context.
//
// Per-nexthop counters register in the object's link counter registry,
// so every linking module counts through its own per-worker storage.
struct route_fib_object {
	struct cp_object cp_object;

	struct route_fib fib;
};
