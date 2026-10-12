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

/*
 * Outcome of one table lookup.
 */
enum route_fib_lookup {
	// No prefix of the table covers the key.
	ROUTE_FIB_LOOKUP_MISS,
	// The covering prefix selects an empty route list: nothing to
	// forward through.
	ROUTE_FIB_LOOKUP_EMPTY,
	// out_route carries the selected route.
	ROUTE_FIB_LOOKUP_ROUTE,
};

/*
 * The public handler of the FIB object type: resolves one destination
 * to the action of forwarding through it - the selected nexthop by
 * value, carrying its destination and source ethernet addresses and
 * the device index of the module that runs the table.
 *
 * The key is packet agnostic: the family comes from the address
 * length (4 or 16), and the hash drives the selection inside a
 * multi-nexthop list, so any addressable key resolves, a packet or a
 * test key alike. The caller keeps everything the result does not
 * carry: the hop limit check and repair, the device index to entry
 * resolution of its own context, and the counters of its own
 * registries.
 */
static inline enum route_fib_lookup
route_fib_lookup_route(
	const struct route_fib *fib,
	uint32_t addr_len,
	const uint8_t *addr,
	uint32_t hash,
	struct route *out_route
) {
	uint32_t route_list_id = LPM_VALUE_INVALID;
	if (addr_len == 4) {
		route_list_id = lpm_lookup(&fib->lpm_v4, 4, addr);
	} else if (addr_len == 16) {
		route_list_id = lpm_lookup(&fib->lpm_v6, 16, addr);
	} else {
		return ROUTE_FIB_LOOKUP_MISS;
	}
	if (route_list_id == LPM_VALUE_INVALID) {
		return ROUTE_FIB_LOOKUP_MISS;
	}

	struct route_list *route_lists = ADDR_OF(&fib->route_lists);
	struct route_list *route_list = &route_lists[route_list_id];
	if (route_list->count == 0) {
		return ROUTE_FIB_LOOKUP_EMPTY;
	}

	// TODO: Route selection should be based on hash/NUMA/dp
	// instance/etc
	//
	// The high word of the 32-bit packet hash scaled by the list
	// size spreads the hash uniformly over the list.
	uint64_t *route_indexes = ADDR_OF(&fib->route_indexes);
	uint64_t route_index = route_indexes
		[route_list->start +
		 (((uint64_t)hash * route_list->count) >> 32)];

	struct route *routes = ADDR_OF(&fib->routes);
	*out_route = routes[route_index];
	return ROUTE_FIB_LOOKUP_ROUTE;
}

// A FIB published as a shared object, registered under its type and name
// and reachable from a linking module's execution context.
//
// Per-nexthop counters register in the object's link counter registry,
// so every linking module counts through its own per-worker storage.
struct route_fib_object {
	struct cp_object cp_object;

	struct route_fib fib;
};
