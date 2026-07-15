#pragma once

#include "common/network.h"
#include "filter/rule.h"
#include "lib/errors/errors.h"

struct cp_module;
struct agent;
struct virtual_service;

/*
 * Control-plane descriptors used to build the shared-memory configuration.
 *
 * These structures are prefixed l3b_ to distinguish them from the unprefixed
 * dataplane structures they are compiled into.
 */

/*
 * Source-side match criteria of a virtual service: the IPv6/IPv4 networks and
 * L4 port ranges a packet may originate from.
 */
struct l3b_source_filter_rule {
	struct filter_net6s net6s;
	struct filter_net4s net4s;
	struct filter_port_ranges port_ranges;
};

/*
 * A single backend reached through an IP-in-IP tunnel.
 */
struct l3b_real_server {
	// Outer tunnel address family (ip_family_ip4 or ip_family_ip6).
	enum ip_family type;
	// Real server destination address used as the outer destination.
	struct net_addr destination_addr;
	// Source network used to derive the outer source address.
	struct net source_net;
};

/*
 * A virtual service: the source filter rules that select its traffic, the
 * backends it dispatches to, and the scheduler masks applied to the packet
 * hash to pick a backend through the real-server ring.
 */
struct l3b_virtual_service {
	struct l3b_source_filter_rule *source_filter_rules;
	uint32_t source_filter_rule_count;

	struct l3b_real_server *real_servers;
	uint32_t real_server_count;

	uint32_t hash_mask;
	uint32_t index_mask;

	// Capacity of the real server ring; set at configuration time. The ring
	// starts empty and is populated via l3b_virtual_service_update_ring.
	uint32_t ring_capacity;
};

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

void
l3b_module_config_free(struct cp_module *config);

// Allocate a virtual service in shared memory from its control-plane
// descriptor and return a handle (pointer to the relative-pointer slot that
// owns it), so the service can be installed or swapped transiently.
struct virtual_service **
l3b_module_config_add_virtual_service(
	struct cp_module *cp_module,
	const struct l3b_virtual_service *virtual_service,
	yanet_error **err
);

// Publish the virtual services referenced by handles and compile the
// destination filters that route packets to them. Each destination filter
// rule's virtual_service_index selects a slot in the handles array.
int
l3b_module_config_update(
	struct cp_module *cp_module,
	const struct l3b_destination_filter_rule *destination_filter_rules,
	uint32_t destination_filter_rule_count,
	struct virtual_service ***virtual_services,
	uint32_t virtual_service_count,
	yanet_error **err
);
