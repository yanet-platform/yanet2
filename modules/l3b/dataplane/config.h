#pragma once

#include "common/value.h"

#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"

#include "lib/controlplane/config/cp_module.h"

/*
 * Destination classification joins network and transport restrictions
 * within one address family, then selects the first matching rule.
 *
 * The transport attribute is the sixteen bit protocol domain the rules
 * address: the protocol number in the high byte, the transport specific
 * byte (TCP flags, ICMP and ICMPv6 type) in the low one. The classifier
 * holds the class line over the whole domain, and the dataplane leaf
 * composes the key out of the packet.
 */
struct l3b_destination_classifier_ip4 {
	struct classify_attr_net4 dst_attr;
	struct classify_attr_port proto_attr;
	struct value_table root_joint;
	struct vline rule_map;
};

struct l3b_destination_classifier_ip6 {
	struct classify_attr_net6 dst_attr;
	struct classify_attr_port proto_attr;
	struct value_table root_joint;
	struct vline rule_map;
};

/*
 * Top-level l3b module configuration published into shared memory.
 *
 * The classifiers select a destination rule whose object link resolves the
 * named virtual service in the worker's execution context. They stay zeroed
 * until a rule is installed and must not be queried without rules: their
 * relative pointers have no compiled targets yet.
 */
struct module_config {
	struct cp_module cp_module;

	uint32_t destination_filter_rule_count;
	// Each destination rule resolves through its module object link.
	uint64_t *rule_object_links;
	// Each rule counts link packets only if the service publishes that
	// counter.
	uint64_t *rule_link_counter_ids;

	struct l3b_destination_classifier_ip6 classifier_ip6;
	struct l3b_destination_classifier_ip4 classifier_ip4;
};
