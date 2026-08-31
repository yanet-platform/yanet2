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
 * ranges and the virtual service a matching packet is forwarded to.
 */
struct l3b_destination_filter_rule {
	struct filter_net6s net6s;
	struct filter_net4s net4s;
	struct filter_proto_ranges proto_ranges;
	uint32_t virtual_service_index;
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

// Publish the virtual services named by service_names and compile the
// destination filters that route packets to them. Each name is linked through
// cp_module_link_object in array order; each destination filter rule's
// virtual_service_index selects a slot in that array.
int
l3b_module_config_update(
	struct cp_module *cp_module,
	const struct l3b_destination_filter_rule *destination_filter_rules,
	uint32_t destination_filter_rule_count,
	const char *const *service_names,
	uint32_t service_count,
	yanet_error **err
);
