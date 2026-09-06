#pragma once

#include <lib/filter/filter.h>
#include <lib/filter/rule.h>

#include "lib/controlplane/config/cp_module.h"

/*
 * Top-level l3b module configuration published into shared memory.
 *
 * The module-level filters classify an incoming packet into the index of the
 * matched destination filter rule; rule_object_links holds, per rule, the
 * cp_module object link index through which the per-worker execution context
 * resolves the named virtual service object.
 *
 * Contract: as long as destination_filter_rule_count is greater than zero, the
 * controlplane must filter_init both filter_ip6 and filter_ip4 — the dataplane
 * queries them whenever at least one rule exists.
 */
struct module_config {
	struct cp_module cp_module;

	uint32_t destination_filter_rule_count;
	// Object link index per destination filter rule, into
	// cp_module.objects.
	uint64_t *rule_object_links;

	struct filter filter_ip6;
	struct filter filter_ip4;
};
