#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/network.h"

#include "lib/controlplane/config/defines.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"

#include "modules/route/dataplane/fib.h"

struct agent;
struct cp_module;
struct cp_object;
struct route_module_config;

// Allocate a module config linked to the table object published under the
// same name.
//
// A generation wiring the module into a chain installs only when the
// object is published, so the object goes out first.
struct cp_module *
route_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

// Destroy the module when it is dangling, per cp_module_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the module; the caller must keep its handle and retry later.
int
route_module_config_free(struct cp_module *cp_module, yanet_error **err);

// Registers the module-level counters and records their ids in the config.
//
// The counter registry must already be initialized. Callers that build a
// config without route_module_config_new have to invoke this themselves,
// otherwise the handler reads unregistered counter ids.
int
route_module_config_register_counters(
	struct route_module_config *config, yanet_error **err
);

// Link a device by name and return its index in the module's device table.
//
// A name already linked keeps its index, so the table only ever grows and
// a table object built against it stays valid across relinks.
int
route_module_config_link_device(
	struct cp_module *cp_module,
	const char *device_name,
	uint32_t *index,
	yanet_error **err
);

// A route config as one generation holds it: the module config and, when
// published, its table object, pinned for reading.
struct route_snapshot;

// Pin the generation publishing the named config.
//
// The pin keeps the module config and its table object alive until
// route_snapshot_close. Fails with the not-found kind when no module
// config of that name is published.
struct route_snapshot *
route_snapshot_open(struct agent *agent, const char *name, yanet_error **err);

// Release the pin. Walks taken from the handle must be freed first.
void
route_snapshot_close(struct route_snapshot *snapshot);

// Whether the generation holds a table object for the config.
bool
route_snapshot_has_fib(const struct route_snapshot *snapshot);

// Nexthops the published table holds, 0 without a table object.
uint64_t
route_snapshot_route_count(const struct route_snapshot *snapshot);

// IPv4 ranges a walk of the published table would yield, 0 without a
// table object.
uint64_t
route_snapshot_range_count_v4(const struct route_snapshot *snapshot);

// IPv6 ranges a walk of the published table would yield, 0 without a
// table object.
uint64_t
route_snapshot_range_count_v6(const struct route_snapshot *snapshot);

// Size of the module's device table.
uint64_t
route_snapshot_device_count(const struct route_snapshot *snapshot);

// Name at the given index of the module's device table, or an empty
// string past its end.
const char *
route_snapshot_device_name(
	const struct route_snapshot *snapshot, uint64_t index
);

// Zero-copy walk over a table, reading shared memory in place.
struct fib_iter;

// Open a walk over the published table, borrowing the handle's pin.
//
// Fails with the not-found kind when the generation holds no table
// object for the config.
struct fib_iter *
route_snapshot_fib_iter(struct route_snapshot *snapshot, yanet_error **err);

// Open a walk over an owned object, or NULL on allocation failure.
//
// Device names stay empty: they resolve through the module that runs the
// table, which an owned object does not know.
struct fib_iter *
fib_iter_new(struct cp_object *cp_object);

void
fib_iter_free(struct fib_iter *it);

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

// Returns the nexthop's registered counter name.
//
// Returns an empty string when no counter name was read for the nexthop,
// ordinarily because it isn't individually counted.
const char *
fib_iter_nexthop_counter_name(const struct fib_iter *it, uint64_t nexthop_idx);
