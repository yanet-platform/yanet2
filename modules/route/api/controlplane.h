#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/network.h"

struct agent;
struct cp_module;
struct memory_context;
struct route_module_config;

// Zero-copy FIB iterator.
//
// Walks IPv4 and IPv6 LPM trees sequentially. Each call to
// fib_iter_next() advances to the next LPM range.
//
// Nexthop data (MAC addresses, device names) is resolved on demand directly
// from shared memory without heap allocation.
struct fib_iter;

struct cp_module *
route_module_config_create(struct agent *agent, const char *name);

void
route_module_config_free(struct cp_module *cp_module);

int
route_module_config_data_init(
	struct route_module_config *config,
	struct memory_context *memory_context
);

int
route_module_config_add_route(
	struct cp_module *cp_module,
	struct ether_addr dst_addr,
	struct ether_addr src_addr,
	const char *device_name
);

int
route_module_config_add_route_list(
	struct cp_module *cp_module, size_t count, const uint32_t *indexes
);

int
route_module_config_add_prefix_v4(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);

int
route_module_config_add_prefix_v6(
	struct cp_module *cp_module,
	const uint8_t *from,
	const uint8_t *to,
	uint32_t route_list_index
);

// Create a FIB iterator for the given module config.
//
// Returns NULL on allocation failure.
struct fib_iter *
fib_iter_create(struct cp_module *cp_module);

// Destroy a FIB iterator created by fib_iter_create.
void
fib_iter_destroy(struct fib_iter *it);

// Advance to the next LPM range.
//
// Returns true if a new entry is available, false when iteration is complete.
bool
fib_iter_next(struct fib_iter *it);

// Returns address family of the current entry: 4 or 6.
uint8_t
fib_iter_address_family(const struct fib_iter *it);

// Returns a pointer to the prefix range start (4 or 16 bytes).
const uint8_t *
fib_iter_prefix_from(const struct fib_iter *it);

// Returns a pointer to the prefix range end (4 or 16 bytes).
const uint8_t *
fib_iter_prefix_to(const struct fib_iter *it);

// Returns the number of ECMP nexthops for the current entry.
uint64_t
fib_iter_nexthop_count(const struct fib_iter *it);

// Copies the destination MAC of the i-th nexthop into dst.
void
fib_iter_nexthop_dst_mac(
	const struct fib_iter *it, uint64_t nexthop_idx, struct ether_addr *dst
);

// Copies the source MAC of the i-th nexthop into dst.
void
fib_iter_nexthop_src_mac(
	const struct fib_iter *it, uint64_t nexthop_idx, struct ether_addr *dst
);

// Returns a pointer to the device name of the i-th nexthop.
const char *
fib_iter_nexthop_device_name(const struct fib_iter *it, uint64_t nexthop_idx);
