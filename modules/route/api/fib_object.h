#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/network.h"

#include "lib/errors/errors.h"

#include "modules/route/dataplane/fib.h"

struct agent;
struct cp_object;

// Allocate an empty FIB object registered under the FIB type and the given
// name once published. The object starts dangling, owned by the caller.
struct cp_object *
route_fib_object_new(struct agent *agent, const char *name, yanet_error **err);

// Destroy the object when it is dangling, per cp_object_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the object; the caller must keep its handle and retry later.
int
route_fib_object_free(struct cp_object *cp_object, yanet_error **err);

// Append a nexthop and return its index, or -1 on failure.
//
// The device index is stored as given and resolves through the device
// links of the module that runs the table, so keeping it within that
// module's link table is the caller's contract. A NULL or empty counter
// name leaves the nexthop uncounted, otherwise it names a packet/byte
// counter registered in the object's link counter registry, so each
// linking module counts through its own per-worker storage.
int
route_fib_object_add_route(
	struct cp_object *cp_object,
	struct ether_addr dst_addr,
	struct ether_addr src_addr,
	uint64_t device_index,
	const char *counter_name,
	yanet_error **err
);

int
route_fib_object_add_route_list(
	struct cp_object *cp_object, size_t count, const uint32_t *indexes
);

int
route_fib_object_add_prefix_v4(
	struct cp_object *cp_object,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);

int
route_fib_object_add_prefix_v6(
	struct cp_object *cp_object,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);

// The table behind the object, for walks and lookups.
const struct route_fib *
route_fib_object_fib(const struct cp_object *cp_object);

// Name of a nexthop counter registered on the object, or an empty string
// for an uncounted nexthop or an id the registry does not hold.
const char *
route_fib_object_counter_name(
	const struct cp_object *cp_object, uint64_t counter_id
);

// Number of nexthops the object holds.
uint64_t
route_fib_object_route_count(const struct cp_object *cp_object);

// Number of IPv4 ranges a walk of the object's table would yield.
uint64_t
route_fib_object_range_count_v4(const struct cp_object *cp_object);

// Number of IPv6 ranges a walk of the object's table would yield.
uint64_t
route_fib_object_range_count_v6(const struct cp_object *cp_object);
