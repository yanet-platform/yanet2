#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/lpm.h"
#include "common/network.h"

#include "modules/route/dataplane/fib.h"

struct memory_context;

// Initialize an empty table whose trees and arrays allocate from the given
// context. On failure nothing is left allocated.
int
route_fib_init(struct route_fib *fib, struct memory_context *memory_context);

// Release everything the table allocated from the given context.
void
route_fib_fini(struct route_fib *fib, struct memory_context *memory_context);

// Append a nexthop and return its index, or -1 when the array cannot grow.
//
// The device index and counter id are stored as given: resolving a device
// name to an index and registering a counter belong to the owner, whose
// link table and counter registry the table does not know.
int
route_fib_add_route(
	struct route_fib *fib,
	struct memory_context *memory_context,
	struct ether_addr dst_addr,
	struct ether_addr src_addr,
	uint64_t device_index,
	uint64_t counter_id
);

// Append a route list selecting among the given nexthop indexes and return
// its index, or -1 when an array cannot grow.
int
route_fib_add_route_list(
	struct route_fib *fib,
	struct memory_context *memory_context,
	size_t count,
	const uint32_t *indexes
);

// Map an inclusive IPv4 address range to a route list. Returns -1 when the
// tree cannot grow.
int
route_fib_add_prefix_v4(
	struct route_fib *fib,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);

// Map an inclusive IPv6 address range to a route list. Returns -1 when the
// tree cannot grow.
int
route_fib_add_prefix_v6(
	struct route_fib *fib,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);

// Count the IPv4 ranges a walk of the table would yield, without
// materializing them.
uint64_t
route_fib_range_count_v4(const struct route_fib *fib);

// Count the IPv6 ranges a walk of the table would yield, without
// materializing them.
uint64_t
route_fib_range_count_v6(const struct route_fib *fib);

// Return the number of nexthops of a route list, or 0 for an index the
// table does not hold.
uint64_t
route_fib_nexthop_count(const struct route_fib *fib, uint32_t route_list_id);

// Return the i-th nexthop of a route list, or NULL when either index is out
// of range.
const struct route *
route_fib_resolve_route(
	const struct route_fib *fib,
	uint32_t route_list_id,
	uint64_t nexthop_idx
);

enum route_fib_iter_phase {
	route_fib_iter_phase_start = 0,
	route_fib_iter_phase_ipv4 = 4,
	route_fib_iter_phase_ipv6 = 6,
	route_fib_iter_phase_done = 0xff,
};

// Zero-copy walk over the ranges of both trees, IPv4 first, reading them
// in place from shared memory.
struct route_fib_iter {
	const struct route_fib *fib;
	struct lpm_iter lpm_it;
	enum route_fib_iter_phase phase;
};

void
route_fib_iter_init(struct route_fib_iter *it, const struct route_fib *fib);

// Advance to the next range. Returns false once both trees are exhausted.
bool
route_fib_iter_next(struct route_fib_iter *it);

// Address family of the current range: 4 or 6.
uint8_t
route_fib_iter_address_family(const struct route_fib_iter *it);

// Start of the current range, 4 or 16 bytes.
const uint8_t *
route_fib_iter_prefix_from(const struct route_fib_iter *it);

// End of the current range, 4 or 16 bytes.
const uint8_t *
route_fib_iter_prefix_to(const struct route_fib_iter *it);

uint32_t
route_fib_iter_route_list_id(const struct route_fib_iter *it);
