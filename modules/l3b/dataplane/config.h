#pragma once

#include "common/value.h"

#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"

#include "lib/controlplane/config/cp_module.h"

/*
 * Destination classifier of one family: the destination network attribute
 * joined with the transport attribute, with the projection decoder folded
 * in.
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

// Destination classifier of the IPv6 family, in the same shape as the
// IPv4 one.
struct l3b_destination_classifier_ip6 {
	struct classify_attr_net6 dst_attr;
	struct classify_attr_port proto_attr;
	struct value_table root_joint;
	struct vline rule_map;
};

/*
 * Top-level l3b module configuration published into shared memory.
 *
 * The module-level classifiers resolve an incoming packet into the index of
 * the matched destination rule; rule_object_links holds, per rule, the
 * cp_module object link index through which the per-worker execution context
 * resolves the named virtual service object.
 *
 * Contract: the classifiers stay zeroed until the first destination rule is
 * installed, and the dataplane queries them only while at least one rule
 * exists — a lookup over a zeroed attribute reads through a relative
 * pointer the compile never published.
 */
struct module_config {
	struct cp_module cp_module;

	uint32_t destination_filter_rule_count;
	// Object link index per destination filter rule, into
	// cp_module.objects.
	uint64_t *rule_object_links;
	// Link packets counter registry id per rule; COUNTER_INVALID when the
	// linked service publishes none.
	uint64_t *rule_link_counter_ids;

	struct l3b_destination_classifier_ip6 classifier_ip6;
	struct l3b_destination_classifier_ip4 classifier_ip4;
};
