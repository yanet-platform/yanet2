#pragma once

#include <lib/filter/filter.h>
#include <lib/filter/rule.h>

#include "lib/controlplane/config/cp_module.h"

/*
 * Top-level l3b module configuration published into shared memory.
 *
 * The module-level filters classify an incoming packet into a virtual service
 * index; virtual_service_links holds, per service slot, the cp_module object
 * link index through which the per-worker execution context resolves the
 * service object.
 *
 * The filter query returns the index of the matched destination filter rule;
 * virtual_service_indexes maps that rule index to a virtual service slot.
 *
 * Contract: as long as virtual_service_count is greater than zero, the
 * controlplane must filter_init both filter_ip6 and filter_ip4 — the
 * dataplane queries them whenever at least one service exists.
 */
struct module_config {
	struct cp_module cp_module;

	uint32_t virtual_service_count;
	// Object link index per service slot, into cp_module.objects.
	uint64_t *virtual_service_links;

	// One virtual service slot per destination filter rule.
	uint32_t virtual_service_index_count;
	uint32_t *virtual_service_indexes;

	struct filter filter_ip6;
	struct filter filter_ip4;
};
