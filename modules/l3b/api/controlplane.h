#pragma once

#include <lib/filter/rule.h>

#include "common/network.h"
#include "lib/errors/errors.h"

struct cp_module;
struct agent;

/*
 * Control-plane descriptors used to build the shared-memory configuration.
 *
 * These structures are prefixed l3b_ to distinguish them from the unprefixed
 * dataplane structures they are compiled into. Virtual service descriptors
 * live with the object, in objects/l3b/api.
 */

/*
 * A destination-side classification rule: the IPv6/IPv4 networks, protocol
 * ranges and the name of the virtual service a matching packet is forwarded
 * to.
 */
struct l3b_destination_filter_rule {
	struct filter_net6s net6s;
	struct filter_net4s net4s;
	struct filter_proto_ranges proto_ranges;
	// Name of the linked virtual service object.
	const char *virtual_service;
};

struct cp_module *
l3b_module_config_new(
	struct agent *agent, const char *name, yanet_error **error
);

// Destroy the module configuration unless a live configuration generation
// still references it; a refused destroy is reported through err and the
// caller must retry later.
int
l3b_module_config_free(struct cp_module *config, yanet_error **err);

// Compile the destination filters and link the virtual service each rule
// names through cp_module_link_object. Rules naming the same service share one
// link; the dataplane resolves each rule's link to the service object at
// execution time.
int
l3b_module_config_update(
	struct cp_module *cp_module,
	const struct l3b_destination_filter_rule *destination_filter_rules,
	uint32_t destination_filter_rule_count,
	yanet_error **err
);
